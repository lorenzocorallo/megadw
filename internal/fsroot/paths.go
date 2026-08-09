package fsroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ConflictRename    = "rename"
	ConflictOverwrite = "overwrite"
	ConflictFail      = "fail"
)

// ResolveConflict returns a safe destination for an already-sanitized
// relative path. Rename preserves the extension and uses deterministic
// "name (n).ext" candidates.
func (r *Root) ResolveConflict(relative, policy string) (string, error) {
	target, err := r.Prepare(relative)
	if err != nil {
		return "", err
	}
	info, statErr := os.Lstat(target)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect destination: %w", statErr)
	}
	if exists && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: destination is a symlink", ErrSymlink)
	}
	if !exists || policy == ConflictOverwrite {
		return target, nil
	}
	switch policy {
	case ConflictFail:
		return "", fmt.Errorf("%w: %s", ErrConflict, relative)
	case ConflictRename:
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: destination is a symlink", ErrSymlink)
		}
		parent := filepath.Dir(target)
		base := filepath.Base(target)
		ext := filepath.Ext(base)
		if ext == base {
			ext = ""
		}
		stem := base[:len(base)-len(ext)]
		for index := 1; index <= 100_000; index++ {
			candidateName := stem + " (" + strconv.Itoa(index) + ")" + ext
			candidateRel, err := filepath.Rel(r.path, filepath.Join(parent, candidateName))
			if err != nil {
				return "", fmt.Errorf("build conflict candidate: %w", err)
			}
			candidate, err := r.Prepare(candidateRel)
			if err != nil {
				return "", err
			}
			if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			} else if err != nil {
				return "", fmt.Errorf("inspect conflict candidate: %w", err)
			}
		}
		return "", fmt.Errorf("%w: exhausted rename candidates", ErrConflict)
	default:
		return "", fmt.Errorf("invalid conflict policy %q", policy)
	}
}

// PlanConflict performs the same collision and symlink checks without
// creating the configured root or parent directories. It is used while a job
// is being queued; the writer must call Prepare again immediately before
// touching a file because another job may win the race afterwards.
func (r *Root) PlanConflict(relative, policy string) (string, error) {
	target, err := r.Join(relative)
	if err != nil {
		return "", err
	}
	if err := checkExistingParents(r.path, filepath.Dir(target)); err != nil {
		return "", err
	}
	info, statErr := os.Lstat(target)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect destination: %w", statErr)
	}
	if exists && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: destination is a symlink", ErrSymlink)
	}
	if !exists || policy == ConflictOverwrite {
		return target, nil
	}
	switch policy {
	case ConflictFail:
		return "", fmt.Errorf("%w: %s", ErrConflict, relative)
	case ConflictRename:
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: destination is a symlink", ErrSymlink)
		}
		parent := filepath.Dir(target)
		base := filepath.Base(target)
		ext := filepath.Ext(base)
		if ext == base {
			ext = ""
		}
		stem := base[:len(base)-len(ext)]
		for index := 1; index <= 100_000; index++ {
			candidateName := stem + " (" + strconv.Itoa(index) + ")" + ext
			candidateRel, err := filepath.Rel(r.path, filepath.Join(parent, candidateName))
			if err != nil {
				return "", fmt.Errorf("build conflict candidate: %w", err)
			}
			candidate, err := r.Join(candidateRel)
			if err != nil {
				return "", err
			}
			if err := checkExistingParents(r.path, filepath.Dir(candidate)); err != nil {
				return "", err
			}
			if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			} else if err != nil {
				return "", fmt.Errorf("inspect conflict candidate: %w", err)
			}
		}
		return "", fmt.Errorf("%w: exhausted rename candidates", ErrConflict)
	default:
		return "", fmt.Errorf("invalid conflict policy %q", policy)
	}
}

func checkExistingParents(root, parent string) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%w: parent %q", ErrPathEscape, parent)
	}
	current := root
	if err := checkExistingComponent(current); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		err := checkExistingComponent(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func checkExistingComponent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrSymlink, path)
	}
	if !info.IsDir() {
		return fmt.Errorf("controlled parent %q is not a directory", path)
	}
	return nil
}

// SanitizeDestinationSubdirectory is an explicit name for the browser-supplied
// destination field, making it harder for API handlers to accidentally call a
// less restrictive path helper.
func SanitizeDestinationSubdirectory(value string) (string, error) {
	return SanitizeRelativePath(value)
}
