package download

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lorenzocorallo/megadw/internal/fsroot"
)

const PartialSuffix = ".mega.part"

// PartialFile is the one sparse random-access file used for a remote file.
// It never creates per-segment temporary files and never performs a merge.
type PartialFile struct {
	file *os.File
	path string
	size int64
}

// PartialRelativePath returns the job-scoped relative path for one remote
// file. remotePath must already represent the sanitized remote tree; it is
// validated again at this boundary before touching disk.
func PartialRelativePath(jobID, remotePath string) (string, error) {
	job, err := fsroot.SanitizeComponent(jobID)
	if err != nil {
		return "", fmt.Errorf("invalid job id: %w", err)
	}
	path, err := fsroot.SanitizeRelativePath(remotePath)
	if err != nil {
		return "", fmt.Errorf("invalid remote path: %w", err)
	}
	if path == "" {
		return "", fmt.Errorf("invalid remote path: %w", fsroot.ErrInvalidPath)
	}
	return filepath.Join(job, path+PartialSuffix), nil
}

// OpenPartialFile opens or creates a sparse partial file and sets its logical
// length to size. Existing data is preserved up to that length.
func OpenPartialFile(root *fsroot.Root, jobID, remotePath string, size int64) (*PartialFile, bool, error) {
	if size < 0 {
		return nil, false, fmt.Errorf("partial file size must not be negative")
	}
	relative, err := PartialRelativePath(jobID, remotePath)
	if err != nil {
		return nil, false, err
	}
	path, err := root.Prepare(relative)
	if err != nil {
		return nil, false, err
	}
	_, statErr := os.Lstat(path)
	existed := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("inspect partial file: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open partial file: %w", err)
	}
	closeOnError := func(err error) (*PartialFile, bool, error) {
		_ = file.Close()
		return nil, existed, err
	}
	if err := file.Truncate(size); err != nil {
		return closeOnError(fmt.Errorf("size partial file: %w", err))
	}
	return &PartialFile{file: file, path: path, size: size}, existed, nil
}

// OpenPartial is a concise alias for OpenPartialFile.
func OpenPartial(root *fsroot.Root, jobID, remotePath string, size int64) (*PartialFile, bool, error) {
	return OpenPartialFile(root, jobID, remotePath, size)
}

func (p *PartialFile) Path() string {
	if p == nil {
		return ""
	}
	return p.path
}

func (p *PartialFile) Size() int64 {
	if p == nil {
		return 0
	}
	return p.size
}

func (p *PartialFile) WriteAt(data []byte, offset int64) (int, error) {
	if p == nil || p.file == nil {
		return 0, fmt.Errorf("partial file is closed")
	}
	if offset < 0 || offset > p.size || int64(len(data)) > p.size-offset {
		return 0, fmt.Errorf("partial write is outside file bounds")
	}
	return p.file.WriteAt(data, offset)
}

func (p *PartialFile) Sync() error {
	if p == nil || p.file == nil {
		return fmt.Errorf("partial file is closed")
	}
	return p.file.Sync()
}

func (p *PartialFile) Close() error {
	if p == nil || p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	return err
}
