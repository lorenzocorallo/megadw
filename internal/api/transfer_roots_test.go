package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateTransferRootClosesDescriptor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "transfer")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	before := apiOpenDescriptorCount(t)
	for range 64 {
		if err := validateTransferRoot(root); err != nil {
			t.Fatal(err)
		}
	}
	after := apiOpenDescriptorCount(t)
	if after != before {
		t.Fatalf("open descriptors after root validation = %d, want %d", after, before)
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
