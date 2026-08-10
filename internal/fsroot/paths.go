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

// ResolveConflict chooses a relative destination for planning and API
// metadata only. It does not reserve that name. Finalization must use
// RenameFrom with an atomic no-replace rename for the chosen name.
func (r *Root) ResolveConflict(relative, policy string) (string, error) {
	return r.PlanConflict(relative, policy)
}

// PlanConflict performs an anchored metadata lookup and returns a sanitized
// relative candidate. The result is deliberately not a pathname capability;
// callers must use RenameFrom/OpenFile/etc. for the subsequent operation.
func (r *Root) PlanConflict(relative, policy string) (string, error) {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return "", err
	}
	if safe == "" {
		return "", fmt.Errorf("%w: destination file is required", ErrInvalidPath)
	}
	if policy != ConflictRename && policy != ConflictOverwrite && policy != ConflictFail {
		return "", fmt.Errorf("invalid conflict policy %q", policy)
	}

	info, statErr := r.Lstat(safe)
	if errors.Is(statErr, os.ErrNotExist) {
		return safe, nil
	}
	if statErr != nil {
		return "", fmt.Errorf("inspect destination: %w", statErr)
	}
	if info.IsSymlink() {
		return "", fmt.Errorf("%w: destination is a symlink", ErrSymlink)
	}
	if policy == ConflictOverwrite {
		return safe, nil
	}
	if policy == ConflictFail {
		return "", fmt.Errorf("%w: %s", ErrConflict, safe)
	}

	parent, base := splitRelative(safe)
	ext := filepath.Ext(base)
	if ext == base {
		ext = ""
	}
	stem := strings.TrimSuffix(base, ext)
	for index := 1; index <= 100_000; index++ {
		candidateName := stem + " (" + strconv.Itoa(index) + ")" + ext
		candidate := filepath.Join(parent, candidateName)
		candidate, err = SanitizeRelativePath(candidate)
		if err != nil {
			return "", fmt.Errorf("build conflict candidate: %w", err)
		}
		candidateInfo, candidateErr := r.Lstat(candidate)
		if errors.Is(candidateErr, os.ErrNotExist) {
			return candidate, nil
		}
		if candidateErr != nil {
			return "", fmt.Errorf("inspect conflict candidate: %w", candidateErr)
		}
		if candidateInfo.IsSymlink() {
			return "", fmt.Errorf("%w: conflict candidate is a symlink", ErrSymlink)
		}
	}
	return "", fmt.Errorf("%w: exhausted rename candidates", ErrConflict)
}

// SanitizeComponent validates one remote or user-provided path component.
// Slashes are rejected here so callers cannot smuggle a second component.
func SanitizeComponent(component string) (string, error) {
	if component == "" || component == "." || component == ".." {
		return "", fmt.Errorf("%w: empty, dot, and dot-dot components are not allowed", ErrInvalidPath)
	}
	if strings.IndexByte(component, 0) >= 0 || strings.ContainsAny(component, `/\\`) {
		return "", fmt.Errorf("%w: component contains a path separator or NUL", ErrInvalidPath)
	}
	return component, nil
}

// SanitizeRelativePath sanitizes a slash-separated path while preserving
// nested directories and Unicode names. Empty is the only accepted empty
// path, which represents the root itself for optional subdirectories.
func SanitizeRelativePath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.IndexByte(value, 0) >= 0 || filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return "", fmt.Errorf("%w: absolute paths, backslashes, and NUL are not allowed", ErrInvalidPath)
	}
	parts := strings.Split(value, "/")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		component, err := SanitizeComponent(part)
		if err != nil {
			return "", err
		}
		clean = append(clean, component)
	}
	return filepath.Join(clean...), nil
}

// SanitizeDestinationSubdirectory is an explicit name for the browser-
// supplied destination field, making it harder for API handlers to call a
// less restrictive path helper accidentally.
func SanitizeDestinationSubdirectory(value string) (string, error) {
	return SanitizeRelativePath(value)
}
