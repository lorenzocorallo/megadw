package fsroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrPathEscape  = errors.New("path escapes configured root")
	ErrSymlink     = errors.New("symlink in controlled path")
	ErrInvalidPath = errors.New("invalid relative path")
	ErrConflict    = errors.New("destination already exists")
)

// Root is a filesystem capability rooted at one configured absolute path.
// All methods validate containment before returning a path and reject symlink
// components under the root.
type Root struct {
	path string
}

func New(path string) (*Root, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: root must be an absolute non-NUL path", ErrInvalidPath)
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: filesystem root is not an application root", ErrInvalidPath)
	}
	return &Root{path: clean}, nil
}

func (r *Root) Path() string {
	if r == nil {
		return ""
	}
	return r.path
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

func (r *Root) Join(relative string) (string, error) {
	if r == nil || r.path == "" {
		return "", fmt.Errorf("%w: root is nil", ErrInvalidPath)
	}
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return "", err
	}
	target := r.path
	if safe != "" {
		target = filepath.Join(r.path, safe)
	}
	rel, err := filepath.Rel(r.path, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, relative)
	}
	return target, nil
}

// Ensure creates the configured root and rejects a symlink at the root.
func (r *Root) Ensure() error {
	if r == nil {
		return fmt.Errorf("%w: root is nil", ErrInvalidPath)
	}
	return ensureDirectoryPath(r.path)
}

// Prepare validates and creates the parent directories for relative. It does
// not create the final file, allowing callers to choose exclusive creation or
// a safe existing-file policy.
func (r *Root) Prepare(relative string) (string, error) {
	target, err := r.Join(relative)
	if err != nil {
		return "", err
	}
	if err := r.Ensure(); err != nil {
		return "", err
	}
	parent := filepath.Dir(target)
	if err := ensureDirectoryPath(parent); err != nil {
		return "", err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: final path is a symlink", ErrSymlink)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect target: %w", err)
	}
	return target, nil
}

// Resolve is the checked path-resolution operation used by callers that need
// a destination path but do not need the more descriptive Prepare name.
func (r *Root) Resolve(relative string) (string, error) {
	return r.Prepare(relative)
}

func ensureDirectoryPath(path string) error {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	separator := string(filepath.Separator)
	prefix := volume
	if strings.HasPrefix(rest, separator) {
		prefix += separator
		rest = strings.TrimPrefix(rest, separator)
	}
	if prefix == "" {
		prefix = "."
	}
	for _, part := range strings.Split(rest, separator) {
		if part == "" || part == "." {
			continue
		}
		if prefix == separator || strings.HasSuffix(prefix, separator) {
			prefix += part
		} else {
			prefix = filepath.Join(prefix, part)
		}
		info, err := os.Lstat(prefix)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(prefix, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("create controlled directory %q: %w", prefix, err)
			}
			info, err = os.Lstat(prefix)
		}
		if err != nil {
			return fmt.Errorf("inspect controlled directory %q: %w", prefix, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, prefix)
		}
		if !info.IsDir() {
			return fmt.Errorf("controlled path %q is not a directory", prefix)
		}
	}
	return nil
}
