package download

import (
	"fmt"
	"io"
	"os"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

// VerifyPartialFile verifies a partial file without loading it into memory.
func VerifyPartialFile(path string, size int64, key mega.FileKey) error {
	if size < 0 {
		return fmt.Errorf("negative file size")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open partial file for verification: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat partial file for verification: %w", err)
	}
	if info.Size() != size {
		return fmt.Errorf("%w: partial file size %d, want %d", mega.ErrIntegrityMismatch, info.Size(), size)
	}
	if err := mega.VerifyIntegrity(io.LimitReader(file, size), size, key); err != nil {
		return err
	}
	return nil
}
