package download

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestValidateTransferRootsClosesDescriptors(t *testing.T) {
	base := t.TempDir()
	job := store.DownloadJobRecord{
		ID:             "root-validation",
		IncompleteRoot: filepath.Join(base, "partial"),
		CompleteRoot:   filepath.Join(base, "complete"),
	}
	if err := os.MkdirAll(job.IncompleteRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(job.CompleteRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	before := openDescriptorCount(t)
	for range 64 {
		if err := validateTransferRoots(job); err != nil {
			t.Fatal(err)
		}
	}
	after := openDescriptorCount(t)
	if after != before {
		t.Fatalf("open descriptors after root validation = %d, want %d", after, before)
	}
}

func openDescriptorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("open descriptor accounting unavailable: %v", err)
	}
	return len(entries)
}
