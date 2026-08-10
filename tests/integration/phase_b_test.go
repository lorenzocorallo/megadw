package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

func TestPublicFileFixtureResolvesWithoutPayloadDownload(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()

	job, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
	if err != nil {
		t.Fatalf("ResolveLink() error = %v", err)
	}
	if job.Kind != mega.LinkKindFile || len(job.Files) != 1 {
		t.Fatalf("job = %#v", job)
	}
	file := job.Files[0]
	if file.RelativePath != "fixture.txt" {
		t.Fatalf("file path = %q", file.RelativePath)
	}
	if file.Size != int64(len(fixture.Plaintext())) {
		t.Fatalf("file size = %d", file.Size)
	}
	if file.PayloadURL == "" || len(file.FileKey) != 32 {
		t.Fatalf("resolved file omitted payload/key: %#v", file)
	}
	if got := fixture.PayloadRequestCount(); got != 0 {
		t.Fatalf("payload requests = %d, want 0", got)
	}
}

func TestPublicFolderFixtureResolvesRecursively(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()

	job, err := fixture.Client().ResolveLink(context.Background(), fixture.FolderLink(), "")
	if err != nil {
		t.Fatalf("ResolveLink() error = %v", err)
	}
	if job.Kind != mega.LinkKindFolder || job.DisplayName != "Fixture root" {
		t.Fatalf("job metadata = %#v", job)
	}
	if len(job.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(job.Files))
	}
	file := job.Files[0]
	if file.RelativePath != "Fixture root/Nested folder/fixture.txt" {
		t.Fatalf("relative path = %q", file.RelativePath)
	}
	if job.TotalBytes != int64(len(fixture.Plaintext())) {
		t.Fatalf("total bytes = %d", job.TotalBytes)
	}
	if fixture.PayloadRequestCount() != 0 {
		t.Fatalf("payload requests = %d, want 0 during metadata resolution", fixture.PayloadRequestCount())
	}
	if fixture.CommandRequestCount() != 2 {
		t.Fatalf("command requests = %d, want folder listing plus payload URL", fixture.CommandRequestCount())
	}
}

func TestPublicFolderSelectedNodeResolvesOnlySelectedSubtree(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	client := mega.NewClient(fixture.HTTPClient(), fixture.APIBaseURL())

	selectedFile, err := client.ResolveLink(context.Background(), fixture.FolderLink()+"/file/"+fakeFileHandle, "")
	if err != nil {
		t.Fatal(err)
	}
	if selectedFile.DisplayName != "fixture.txt" || len(selectedFile.Files) != 1 || selectedFile.Files[0].RelativePath != "fixture.txt" {
		t.Fatalf("selected file resolution = %#v", selectedFile)
	}

	selectedFolder, err := client.ResolveLink(context.Background(), fixture.FolderLink()+"/folder/"+fakeNestedHandle, "")
	if err != nil {
		t.Fatal(err)
	}
	if selectedFolder.DisplayName != "Nested folder" || len(selectedFolder.Files) != 1 || selectedFolder.Files[0].RelativePath != "Nested folder/fixture.txt" {
		t.Fatalf("selected folder resolution = %#v", selectedFolder)
	}
}

func TestRangedEncryptedFixtureDecryptsAtAlignedAndUnalignedOffsets(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	job, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
	if err != nil {
		t.Fatalf("ResolveLink() error = %v", err)
	}
	file := job.Files[0]
	plain := fixture.Plaintext()

	for _, offset := range []int64{32, 37} {
		const length int64 = 257
		end := offset + length - 1
		request, err := http.NewRequest(http.MethodGet, file.PayloadURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
		response, err := fixture.HTTPClient().Do(request)
		if err != nil {
			t.Fatalf("range request at %d: %v", offset, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusPartialContent {
			t.Fatalf("range status at %d = %d", offset, response.StatusCode)
		}
		wantRange := "bytes " + strconv.FormatInt(offset, 10) + "-" + strconv.FormatInt(end, 10) + "/" + strconv.Itoa(len(plain))
		if got := response.Header.Get("Content-Range"); got != wantRange {
			t.Fatalf("Content-Range at %d = %q, want %q", offset, got, wantRange)
		}
		decrypted, err := mega.DecryptAt(body, file.Key, offset)
		if err != nil {
			t.Fatalf("DecryptAt(%d): %v", offset, err)
		}
		if string(decrypted) != string(plain[offset:end+1]) {
			t.Fatalf("decrypted range at %d does not match plaintext", offset)
		}
	}
}

func TestFakeFixtureCorruptionAndURLExpiryAreObservable(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{
		ExpirePayloadURL: true,
		CorruptPayload:   true,
		CorruptByteAt:    0,
	})
	defer fixture.Close()

	job, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
	if err != nil {
		t.Fatalf("ResolveLink() error = %v", err)
	}
	file := job.Files[0]
	request, err := http.NewRequest(http.MethodGet, file.PayloadURL, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=0-0")
	response, err := fixture.HTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusGone {
		t.Fatalf("expired payload status = %d, want %d", response.StatusCode, http.StatusGone)
	}

	job, err = fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
	if err != nil {
		t.Fatalf("refresh ResolveLink() error = %v", err)
	}
	request, err = http.NewRequest(http.MethodGet, job.Files[0].PayloadURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", "bytes=0-0")
	response, err = fixture.HTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := mega.DecryptAt(body, job.Files[0].Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted[0] == fixture.Plaintext()[0] {
		t.Fatal("corrupted fixture byte was not changed")
	}
}

func TestFakeFixtureSupportsPayloadFaultModes(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable, 509} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{StatusCode: status, RetryAfter: "3"})
			defer fixture.Close()
			job, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(http.MethodGet, job.Files[0].PayloadURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Range", "bytes=0-7")
			response, err := fixture.HTTPClient().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != status {
				t.Fatalf("status = %d, want %d", response.StatusCode, status)
			}
			if response.Header.Get("Retry-After") != "3" {
				t.Fatalf("Retry-After = %q", response.Header.Get("Retry-After"))
			}
		})
	}

	t.Run("malformed content range", func(t *testing.T) {
		fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{MalformedContentRange: true})
		defer fixture.Close()
		job, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodGet, job.Files[0].PayloadURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Range", "bytes=8-15")
		response, err := fixture.HTTPClient().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if got := response.Header.Get("Content-Range"); got != "bytes 8-8/*" {
			t.Fatalf("Content-Range = %q", got)
		}
	})

	t.Run("connection reset", func(t *testing.T) {
		fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{ResetAfterBytes: 4})
		defer fixture.Close()
		job, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodGet, job.Files[0].PayloadURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Range", "bytes=0-15")
		response, err := fixture.HTTPClient().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr == nil {
			t.Fatal("connection reset was not observable while reading body")
		}
	})
}
