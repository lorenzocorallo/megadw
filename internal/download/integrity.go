package download

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

// VerifyPartialFile verifies a partial file without loading it into memory.
func VerifyPartialFile(path string, size int64, key mega.FileKey) error {
	return VerifyPartialFileContext(context.Background(), path, size, key)
}

// VerifyPartialFileContext makes the sequential integrity scan responsive to
// pause, cancel, and shutdown between its bounded reads.
func VerifyPartialFileContext(ctx context.Context, path string, size int64, key mega.FileKey) error {
	if size < 0 {
		return fmt.Errorf("negative file size")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
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
	reader := &contextReader{ctx: ctx, reader: io.LimitReader(file, size)}
	if err := mega.VerifyIntegrity(reader, size, key); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return err
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
