package download

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/fsroot"
)

func TestFinalizationRenameSurfacesCrossDeviceWithoutCopy(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("/dev/shm is unavailable")
	}
	destinationPath, err := os.MkdirTemp("/dev/shm", "megadw-finalize-")
	if err != nil {
		t.Skipf("cannot create tmpfs test root: %v", err)
	}
	defer os.RemoveAll(destinationPath)

	source, err := fsroot.New(filepath.Join(t.TempDir(), "incomplete"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := fsroot.New(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()

	partial, _, err := source.OpenOrCreateFile("job/file.mega.part", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := partial.Close(); err != nil {
		t.Fatal(err)
	}
	if err := destination.RenameFrom(source, "job/file.mega.part", "file", false); !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("cross-device rename error = %v, want EXDEV", err)
	}
	if _, err := source.Lstat("job/file.mega.part"); err != nil {
		t.Fatalf("source disappeared after EXDEV: %v", err)
	}
	if _, err := destination.Lstat("file"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination was created after EXDEV: %v", err)
	}
}
