package download

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestDeletionRejectsSymlinkedConfiguredRoots(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "job-1"), 0o750); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.bin")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(base, "configured-root")
	if err := os.Symlink(outside, rootLink); err != nil {
		t.Fatal(err)
	}

	job := store.DownloadJobRecord{
		ID:             "job-1",
		CompleteRoot:   rootLink,
		IncompleteRoot: rootLink,
		Files: []store.DownloadFileRecord{{
			State:             string(FileCompleted),
			FinalRelativePath: "victim.bin",
		}},
	}
	if err := removeCompletedFiles(job); err == nil {
		t.Fatal("completed-file deletion accepted a symlinked root")
	}
	if err := (&Manager{}).removePartialDirectory(job); err == nil {
		t.Fatal("partial-directory deletion accepted a symlinked root")
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("outside victim changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "job-1")); err != nil {
		t.Fatalf("outside job directory was removed: %v", err)
	}
}
