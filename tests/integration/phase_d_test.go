package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/app"
	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/fsroot"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestPhaseDSingleWorkerCompletesAndAtomicallyMovesOnePartFile(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-single"
	insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())

	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobCompleted) || record.Files[0].State != string(download.FileCompleted) {
		t.Fatalf("record state = %q/%q", record.State, record.Files[0].State)
	}
	output, err := os.ReadFile(filepath.Join(roots.complete, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, fixture.Plaintext()) {
		t.Fatal("completed output differs from fixture plaintext")
	}
	entries, err := os.ReadDir(filepath.Join(roots.incomplete, jobID))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("job partial directory contains temporary files: %#v", entries)
	}
}

func TestPhaseD100MiBFixtureCompletesWithoutMergeCopy(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 100 << 20})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-100m"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 8<<20)
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(roots.complete, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(fixture.Plaintext())
	if !bytes.Equal(hash.Sum(nil), want[:]) {
		t.Fatal("100 MiB output hash differs from fixture")
	}
	if entries, err := os.ReadDir(filepath.Join(roots.incomplete, jobID)); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("100 MiB partial directory contains temporary files: %#v", entries)
	}
}

func TestPhaseDResumeUsesDurableBitmapAndSkipsFirstSegment(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-resume"
	fileRecord := insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())

	planner, err := download.NewSegmentPlanner(fileRecord.SizeBytes, fileRecord.SegmentSizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	if planner.Count < 2 {
		t.Fatalf("fixture has %d segments, want at least two", planner.Count)
	}
	partialRoot, err := fsroot.New(roots.incomplete)
	if err != nil {
		t.Fatal(err)
	}
	partial, _, err := download.OpenPartialFile(partialRoot, jobID, fileRecord.RemotePath, fileRecord.SizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	segment, err := planner.Segment(0)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := fixture.Client().ResolveLink(context.Background(), fixture.FileLink(), "")
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, metadata.Files[0].PayloadURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", segment.Start, segment.End))
	response, err := fixture.HTTPClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := mega.DecryptAt(ciphertext, metadata.Files[0].Key, segment.Start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := partial.WriteAt(plaintext, segment.Start); err != nil {
		t.Fatal(err)
	}
	if err := partial.Sync(); err != nil {
		t.Fatal(err)
	}
	bitmap, err := download.NewBitmap(planner.Count)
	if err != nil {
		t.Fatal(err)
	}
	if err := bitmap.Set(0); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckpointDownloadFiles(context.Background(), []store.DownloadFileCheckpoint{{
		FileID:          fileRecord.ID,
		CompletedBitmap: bitmap,
		BytesCommitted:  segment.Size(),
		State:           string(download.FilePaused),
		UpdatedAt:       time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := partial.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDownloadJobState(context.Background(), jobID, string(download.JobPaused), time.Now()); err != nil {
		t.Fatal(err)
	}

	before := fixture.PayloadRequestCount()
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	if got, want := fixture.PayloadRequestCount()-before, planner.Count-1; got != want {
		t.Fatalf("resumed payload requests = %d, want %d", got, want)
	}
	output, err := os.ReadFile(filepath.Join(roots.complete, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, fixture.Plaintext()) {
		t.Fatal("resumed output differs from fixture plaintext")
	}
}

func TestPhaseDCorruptionFailsBeforeCompletion(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{CorruptPayload: true, CorruptByteAt: 0})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-corrupt"
	insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())

	if err := manager.RunJob(context.Background(), jobID); err == nil {
		t.Fatal("corrupted download completed successfully")
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobFailed) || record.Files[0].State != string(download.FileFailed) {
		t.Fatalf("corrupt record state = %q/%q", record.State, record.Files[0].State)
	}
	if _, err := os.Stat(filepath.Join(roots.complete, "fixture.txt")); !os.IsNotExist(err) {
		t.Fatalf("corrupt output exists, stat error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(roots.incomplete, jobID))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".part" {
		t.Fatalf("partial entries = %#v, want one .mega.part file", entries)
	}
}

func TestPhaseDSameNameJobsUseIndependentPartialFilesAndFinalConflictNames(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	insertPhaseDJob(t, db, secrets, fixture, roots, "phase-d-name-a", fixture.FileLink())
	insertPhaseDJob(t, db, secrets, fixture, roots, "phase-d-name-b", fixture.FileLink())

	var group sync.WaitGroup
	errors := make(chan error, 2)
	for _, jobID := range []string{"phase-d-name-a", "phase-d-name-b"} {
		group.Add(1)
		go func(id string) {
			defer group.Done()
			errors <- manager.RunJob(context.Background(), id)
		}(jobID)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"fixture.txt", "fixture (1).txt"} {
		output, err := os.ReadFile(filepath.Join(roots.complete, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output, fixture.Plaintext()) {
			t.Fatalf("%s differs from fixture plaintext", name)
		}
	}
}

func TestPhaseDManagerQueueAutoStartsAndRecoversActiveRows(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-queued"
	inserted := insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())
	if err := db.SetDownloadJobState(context.Background(), jobID, string(download.JobDownloading), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: inserted.ID, State: string(download.FileDownloading), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err := db.GetDownloadJob(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if record.State == string(download.JobCompleted) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("recovered queued job state = %q", record.State)
}

func TestPhaseDInterruptedTransferKeepsPartialAndResumes(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{Delay: 50 * time.Millisecond})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-interrupted"
	insertPhaseDJob(t, db, secrets, fixture, roots, jobID, fixture.FileLink())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	err := manager.RunJob(ctx, jobID)
	cancel()
	if err == nil {
		t.Fatal("interrupted transfer returned nil")
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobPaused) {
		t.Fatalf("interrupted job state = %q, want paused", record.State)
	}
	fixture.SetOptions(FakeMegaServerOptions{})
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	record, err = db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobCompleted) {
		t.Fatalf("resumed job state = %q", record.State)
	}
}

func TestPhaseDKillAndRestartResumesPersistedBitmap(t *testing.T) {
	if os.Getenv("MEGAD_PHASE_D_HELPER") == "1" {
		return
	}
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 100 << 20, Delay: 200 * time.Millisecond})
	defer fixture.Close()
	_, db, secrets, roots := newPhaseDManager(t, fixture)
	jobID := "phase-d-process-restart"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 8<<20)
	if err := db.SetDownloadJobState(context.Background(), jobID, string(download.JobQueued), time.Now()); err != nil {
		t.Fatal(err)
	}
	service, err := settings.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	value.Downloads.CheckpointIntervalMs = 60_000
	value.Downloads.CheckpointBytes = 16 << 20
	if err := service.Update(context.Background(), value); err != nil {
		t.Fatal(err)
	}

	first, firstOutput := startPhaseDHelper(t, db.Path(), filepath.Join(filepath.Dir(db.Path()), "secret.key"), fixture.APIBaseURL(), jobID)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		record, readErr := db.GetDownloadJob(context.Background(), jobID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if record.State == string(download.JobFailed) || record.State == string(download.JobCompleted) {
			_ = first.Process.Kill()
			_ = first.Wait()
			t.Fatalf("first worker ended before the forced restart: state=%q output=%s", record.State, firstOutput.String())
		}
		if record.Files[0].BytesCommitted >= 16<<20 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	record, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Files[0].BytesCommitted < 16<<20 || record.State != string(download.JobDownloading) {
		_ = first.Process.Kill()
		_ = first.Wait()
		t.Fatalf("checkpoint was not durable before kill: state=%q bytes=%d", record.State, record.Files[0].BytesCommitted)
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err == nil {
		t.Fatal("killed worker exited successfully")
	}
	record, err = db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != string(download.JobDownloading) {
		t.Fatalf("state after kill = %q, want durable active state", record.State)
	}

	fixture.SetOptions(FakeMegaServerOptions{PayloadSize: 100 << 20})
	second, secondOutput := startPhaseDHelper(t, db.Path(), filepath.Join(filepath.Dir(db.Path()), "secret.key"), fixture.APIBaseURL(), jobID)
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		record, err = db.GetDownloadJob(context.Background(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if record.State == string(download.JobFailed) {
			_ = second.Process.Kill()
			_ = second.Wait()
			t.Fatalf("restart worker failed: %s", secondOutput.String())
		}
		if record.State == string(download.JobCompleted) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.State != string(download.JobCompleted) {
		_ = second.Process.Kill()
		_ = second.Wait()
		t.Fatalf("restart did not complete: state=%q", record.State)
	}
	if err := second.Wait(); err != nil {
		t.Fatalf("restart helper: %v; output=%s", err, secondOutput.String())
	}
	output, err := os.ReadFile(filepath.Join(roots.complete, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, fixture.Plaintext()) {
		t.Fatal("restarted process output differs from fixture")
	}
}

func TestPhaseDWorkerProcess(t *testing.T) {
	if os.Getenv("MEGAD_PHASE_D_HELPER") != "1" {
		return
	}
	stateDir := os.Getenv("MEGAD_PHASE_D_STATE_DIR")
	databasePath := os.Getenv("MEGAD_PHASE_D_DATABASE")
	secretKeyPath := os.Getenv("MEGAD_PHASE_D_SECRET")
	apiURL := os.Getenv("MEGAD_PHASE_D_API")
	jobID := os.Getenv("MEGAD_PHASE_D_JOB")
	application, err := app.Open(context.Background(), app.Config{StateDir: stateDir, DatabasePath: databasePath, SecretKeyPath: secretKeyPath, MegaAPIBaseURL: apiURL})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		record, readErr := application.DB.GetDownloadJob(context.Background(), jobID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if record.State == string(download.JobCompleted) || record.State == string(download.JobFailed) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker helper timed out")
}

func startPhaseDHelper(t *testing.T, databasePath, secretPath, apiURL, jobID string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	output := new(bytes.Buffer)
	command := exec.Command(os.Args[0], "-test.run=^TestPhaseDWorkerProcess$", "-test.v")
	command.Env = append(os.Environ(),
		"MEGAD_PHASE_D_HELPER=1",
		"MEGAD_PHASE_D_STATE_DIR="+filepath.Dir(databasePath),
		"MEGAD_PHASE_D_DATABASE="+databasePath,
		"MEGAD_PHASE_D_SECRET="+secretPath,
		"MEGAD_PHASE_D_API="+apiURL,
		"MEGAD_PHASE_D_JOB="+jobID,
	)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return command, output
}

type phaseDRoots struct {
	incomplete string
	complete   string
}

func newPhaseDManager(t *testing.T, fixture *FakeMegaServer) (*download.Manager, *store.DB, *store.SecretStore, phaseDRoots) {
	t.Helper()
	root := t.TempDir()
	roots := phaseDRoots{incomplete: filepath.Join(root, "incomplete"), complete: filepath.Join(root, "complete")}
	db, err := store.Open(context.Background(), filepath.Join(root, "megad.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	secrets, err := store.OpenSecretStore(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := settings.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := download.NewManager(download.Config{
		DB:                 db,
		Secrets:            secrets,
		Mega:               fixture.Client(),
		Settings:           service,
		CheckpointInterval: time.Second,
		CheckpointBytes:    1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, db, secrets, roots
}

func insertPhaseDJob(t *testing.T, db *store.DB, secrets *store.SecretStore, fixture *FakeMegaServer, roots phaseDRoots, jobID, linkURL string) store.DownloadFileRecord {
	return insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, linkURL, 64<<10)
}

func insertPhaseDJobWithSegment(t *testing.T, db *store.DB, secrets *store.SecretStore, fixture *FakeMegaServer, roots phaseDRoots, jobID, linkURL string, segmentSize int64) store.DownloadFileRecord {
	t.Helper()
	job, err := fixture.Client().ResolveLink(context.Background(), linkURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Files) != 1 {
		t.Fatalf("fixture files = %d", len(job.Files))
	}
	file := job.Files[0]
	planner, err := download.NewSegmentPlanner(file.Size, segmentSize)
	if err != nil {
		t.Fatal(err)
	}
	sourceKey, err := secrets.Encrypt([]byte(mustParsedLinkKey(t, linkURL)))
	if err != nil {
		t.Fatal(err)
	}
	fileKey, err := secrets.Encrypt(file.FileKey)
	if err != nil {
		t.Fatal(err)
	}
	payloadURL, err := secrets.Encrypt([]byte(file.PayloadURL))
	if err != nil {
		t.Fatal(err)
	}
	record, err := db.InsertDownloadJob(context.Background(), store.DownloadJobInput{
		ID:                  jobID,
		SourceKind:          string(job.Kind),
		SourceHandle:        "fixture",
		SourceKeyCiphertext: sourceKey,
		DisplayName:         job.DisplayName,
		TotalBytes:          job.TotalBytes,
		CompleteRoot:        roots.complete,
		IncompleteRoot:      roots.incomplete,
		State:               string(download.JobReady),
		Files: []store.DownloadFileInput{{
			ID:                   jobID + "-file",
			RemoteNodeID:         file.NodeID,
			RemotePath:           file.RelativePath,
			FinalRelativePath:    file.RelativePath,
			SizeBytes:            file.Size,
			SegmentSizeBytes:     segmentSize,
			SegmentCount:         planner.Count,
			FileKeyCiphertext:    fileKey,
			PayloadURLCiphertext: payloadURL,
			State:                string(download.FilePending),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return record.Files[0]
}

func mustParsedLinkKey(t *testing.T, rawURL string) string {
	t.Helper()
	link, err := mega.ParseLink(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return link.Key
}
