package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/settings"
)

func TestValidateTransferRootClosesDescriptor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transfer")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	before := apiOpenDescriptorCount(t)
	for range 64 {
		paths := settings.PathsSettings{
			IncompleteRoot: filepath.Join(root, "partial"),
			CompleteRoot:   filepath.Join(root, "complete"),
		}
		if err := validateTransferRoots(paths); err != nil {
			t.Fatal(err)
		}
	}
	after := apiOpenDescriptorCount(t)
	if after != before {
		t.Fatalf("open descriptors after root validation = %d, want %d", after, before)
	}
}

func TestValidateTransferRootsCreatesMissingRootsOnOneFilesystem(t *testing.T) {
	parent := t.TempDir()
	paths := settings.PathsSettings{
		IncompleteRoot: filepath.Join(parent, "partial"),
		CompleteRoot:   filepath.Join(parent, "complete"),
	}
	if err := validateTransferRoots(paths); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.IncompleteRoot, paths.CompleteRoot} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("validated transfer root %q was not created as a directory: info=%v err=%v", path, info, err)
		}
	}
}

func apiOpenDescriptorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("open descriptor accounting unavailable: %v", err)
	}
	return len(entries)
}
