package download

import (
	"errors"
	"syscall"
	"testing"
)

func TestRenameCompletedFileSurfacesCrossDeviceWithoutCopy(t *testing.T) {
	calls := 0
	err := renameCompletedFile(func(_, _ string) error {
		calls++
		return syscall.EXDEV
	}, "/incomplete/job/file.mega.part", "/complete/file", "file")
	if !errors.Is(err, ErrCrossDevice) {
		t.Fatalf("rename error = %v, want ErrCrossDevice", err)
	}
	if calls != 1 {
		t.Fatalf("rename calls = %d, want one", calls)
	}
}
