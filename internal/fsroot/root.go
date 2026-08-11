//go:build linux

package fsroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

var (
	ErrPathEscape  = errors.New("path escapes configured root")
	ErrSymlink     = errors.New("symlink in controlled path")
	ErrInvalidPath = errors.New("invalid relative path")
	ErrConflict    = errors.New("destination already exists")
	ErrClosed      = errors.New("root is closed")
)

// Root is a capability for one configured directory. New opens an existing
// directory and keeps its descriptor until Close; an absent configured root
// is opened securely on first use. Every operation below the root walks one
// component at a time with O_NOFOLLOW and performs the final operation
// relative to a held directory descriptor.
//
// The configured pathname is retained for diagnostics only. It is never used
// as the security boundary after New returns.
type Root struct {
	mu     sync.RWMutex
	fd     int
	path   string
	closed bool
}

// EntryInfo is metadata obtained with fstatat(2) relative to an anchored
// directory descriptor. In particular, IsSymlink reports the final entry
// without following it.
type EntryInfo struct {
	Mode os.FileMode
	Size int64
}

func (i EntryInfo) IsDir() bool     { return i.Mode&os.ModeDir != 0 }
func (i EntryInfo) IsSymlink() bool { return i.Mode&os.ModeSymlink != 0 }
func (i EntryInfo) IsRegular() bool { return i.Mode&(os.ModeType) == 0 }

const (
	directoryOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fileOpenFlags      = unix.O_CLOEXEC | unix.O_NOFOLLOW
)

// New securely opens path when it exists, or retains a lazy capability when
// the configured root is absent. Missing components are created later by the
// first mutating operation beneath an anchored descriptor. Existing symlinks
// in the configured root path are rejected instead of followed.
func New(path string) (*Root, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: root must be an absolute non-NUL path", ErrInvalidPath)
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: filesystem root is not an application root", ErrInvalidPath)
	}
	fd, err := openAbsoluteDirectory(clean, false)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return nil, fmt.Errorf("open configured root %q: %w", clean, wrapPathError("open root", clean, err))
	}
	return &Root{fd: fd, path: clean}, nil
}

// Close releases the held root descriptor. Existing file descriptors returned
// by OpenFile remain valid, but no new operation may be started afterwards.
func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.fd < 0 {
		return nil
	}
	err := unix.Close(r.fd)
	r.fd = -1
	return err
}

// Path returns the configured pathname for diagnostics and configuration
// display. It must not be used for a security-sensitive filesystem operation.
func (r *Root) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Ensure opens/creates a missing configured root through the same anchored
// component walk used by mutating operations.
func (r *Root) Ensure() error {
	if r == nil {
		return fmt.Errorf("%w: root is nil", ErrInvalidPath)
	}
	fd, err := r.rootDescriptor(true)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

// RequireWritable verifies that the process can create entries below the
// anchored root. It does not create a probe file and therefore remains safe to
// call during settings validation.
func (r *Root) RequireWritable() error {
	if r == nil {
		return fmt.Errorf("%w: root is nil", ErrInvalidPath)
	}
	fd, err := r.rootDescriptor(false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := unix.Faccessat(fd, ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS); err != nil {
		return wrapPathError("check root access", r.path, err)
	}
	return nil
}

// DeviceID returns the filesystem device of the anchored root descriptor.
// Comparing this value before a transfer starts detects configurations that
// cannot support the required atomic final rename.
func (r *Root) DeviceID() (uint64, error) {
	if r == nil {
		return 0, fmt.Errorf("%w: root is nil", ErrInvalidPath)
	}
	fd, err := r.rootDescriptor(false)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return 0, fmt.Errorf("stat root descriptor: %w", err)
	}
	return uint64(stat.Dev), nil
}

// MkdirAll creates relative and all missing parents through held directory
// descriptors. Existing symlinks are rejected.
func (r *Root) MkdirAll(relative string, perm os.FileMode) error {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return err
	}
	fd, err := r.openDirectory(safe, true)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

// OpenFile opens relative beneath the root. O_CREATE causes missing parent
// directories to be created securely. The returned *os.File is backed by the
// descriptor opened relative to the anchored parent; callers never need the
// pathname to use it.
func (r *Root) OpenFile(relative string, flags int, perm os.FileMode) (*os.File, error) {
	file, _, err := r.openFileWithHook(relative, flags, perm, nil, false)
	return file, err
}

// OpenOrCreateFile opens a read/write file and reports whether it already
// existed. The existence decision is made by an O_EXCL create attempt, not by
// a separate pathname stat, so it is safe for resume and concurrent jobs.
func (r *Root) OpenOrCreateFile(relative string, perm os.FileMode) (*os.File, bool, error) {
	return r.openFileWithHook(relative, os.O_RDWR|os.O_CREATE, perm, nil, true)
}

// Lstat returns metadata for relative without following the final entry. All
// parent components are opened relative to held descriptors.
func (r *Root) Lstat(relative string) (EntryInfo, error) {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return EntryInfo{}, err
	}
	parent, name := splitRelative(safe)
	parentFD, err := r.openDirectory(parent, false)
	if err != nil {
		return EntryInfo{}, err
	}
	defer unix.Close(parentFD)
	return lstatAt(parentFD, name, safe)
}

// Remove removes one file or empty directory beneath the root. It never
// follows a symlink and rejects a symlink at the final entry.
func (r *Root) Remove(relative string) error {
	return r.removeWithHook(relative, nil)
}

// RemoveAll recursively removes relative using only descriptor-relative
// operations. Symlink components are rejected; no recursive pathname walk is
// delegated to os.RemoveAll.
func (r *Root) RemoveAll(relative string) error {
	return r.removeAllWithHook(relative, nil)
}

// SyncDir opens a directory relative to the root and fsyncs that descriptor.
// It is used for rename and recovery durability without reopening a pathname.
func (r *Root) SyncDir(relative string) error {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return err
	}
	fd, err := r.openDirectory(safe, false)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(fd), safe)
	if directory == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open directory descriptor for sync")
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// RenameFrom atomically moves sourceRelative from source into this root at
// destinationRelative. The source and destination roots may be unrelated
// pathnames. overwrite selects normal rename replacement; false uses
// renameat2(RENAME_NOREPLACE), which is required for fail/rename conflict
// policies and removes the final check-then-use gap.
func (r *Root) RenameFrom(source *Root, sourceRelative, destinationRelative string, overwrite bool) error {
	return r.renameFromWithHook(source, sourceRelative, destinationRelative, overwrite, nil)
}

func (r *Root) openFileWithHook(relative string, flags int, perm os.FileMode, before func(), reportExisted bool) (*os.File, bool, error) {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return nil, false, err
	}
	if safe == "" {
		return nil, false, fmt.Errorf("%w: a file path is required", ErrInvalidPath)
	}
	parent, name := splitRelative(safe)
	parentFD, err := r.openDirectory(parent, flags&os.O_CREATE != 0)
	if err != nil {
		return nil, false, err
	}
	defer unix.Close(parentFD)
	if before != nil {
		before()
	}

	openFlags := flags | fileOpenFlags
	existed := false
	if reportExisted {
		openFlags |= unix.O_EXCL
	}
	fd, openErr := unix.Openat(parentFD, name, openFlags, uint32(perm.Perm()))
	if reportExisted && openErr != nil && errors.Is(openErr, unix.EEXIST) {
		existed = true
		fd, openErr = unix.Openat(parentFD, name, openFlags&^unix.O_EXCL, uint32(perm.Perm()))
	}
	if openErr != nil {
		return nil, false, wrapPathError("openat", safe, openErr)
	}
	file := os.NewFile(uintptr(fd), safe)
	if file == nil {
		_ = unix.Close(fd)
		return nil, false, fmt.Errorf("openat %q: create file handle", safe)
	}
	return file, existed, nil
}

func (r *Root) openDirectory(relative string, create bool) (int, error) {
	if r == nil {
		return -1, fmt.Errorf("%w: root is nil", ErrInvalidPath)
	}
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return -1, err
	}
	current, err := r.rootDescriptor(create)
	if err != nil {
		return -1, err
	}
	if safe == "" {
		return current, nil
	}
	for _, component := range strings.Split(safe, string(filepath.Separator)) {
		child, openErr := openDirectoryAt(current, component)
		if errors.Is(openErr, unix.ENOENT) && create {
			mkdirErr := unix.Mkdirat(current, component, 0o750)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(current)
				return -1, wrapPathError("mkdirat", safe, mkdirErr)
			}
			child, openErr = openDirectoryAt(current, component)
		}
		if openErr != nil {
			_ = unix.Close(current)
			return -1, wrapPathError("openat", safe, openErr)
		}
		_ = unix.Close(current)
		current = child
	}
	return current, nil
}

func (r *Root) removeWithHook(relative string, before func()) error {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return err
	}
	if safe == "" {
		return fmt.Errorf("%w: cannot remove configured root", ErrInvalidPath)
	}
	parent, name := splitRelative(safe)
	parentFD, err := r.openDirectory(parent, false)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	if before != nil {
		before()
	}
	info, err := lstatAt(parentFD, name, safe)
	if err != nil {
		return err
	}
	if info.IsSymlink() {
		return fmt.Errorf("%w: %s", ErrSymlink, safe)
	}
	flags := 0
	if info.IsDir() {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(parentFD, name, flags); err != nil {
		return wrapPathError("unlinkat", safe, err)
	}
	return nil
}

func (r *Root) removeAllWithHook(relative string, before func()) error {
	safe, err := SanitizeRelativePath(relative)
	if err != nil {
		return err
	}
	if safe == "" {
		return fmt.Errorf("%w: cannot remove configured root", ErrInvalidPath)
	}
	parent, name := splitRelative(safe)
	parentFD, err := r.openDirectory(parent, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer unix.Close(parentFD)
	if before != nil {
		before()
	}
	return removeEntryAt(parentFD, name, safe)
}

func (r *Root) renameFromWithHook(source *Root, sourceRelative, destinationRelative string, overwrite bool, before func()) error {
	if source == nil {
		return fmt.Errorf("%w: source root is nil", ErrInvalidPath)
	}
	sourceSafe, err := SanitizeRelativePath(sourceRelative)
	if err != nil {
		return err
	}
	destinationSafe, err := SanitizeRelativePath(destinationRelative)
	if err != nil {
		return err
	}
	if sourceSafe == "" || destinationSafe == "" {
		return fmt.Errorf("%w: source and destination files are required", ErrInvalidPath)
	}
	sourceParent, sourceName := splitRelative(sourceSafe)
	destinationParent, destinationName := splitRelative(destinationSafe)
	sourceFD, err := source.openDirectory(sourceParent, false)
	if err != nil {
		return err
	}
	defer unix.Close(sourceFD)
	destinationFD, err := r.openDirectory(destinationParent, true)
	if err != nil {
		return err
	}
	defer unix.Close(destinationFD)
	if before != nil {
		before()
	}

	sourceInfo, err := lstatAt(sourceFD, sourceName, sourceSafe)
	if err != nil {
		return err
	}
	if sourceInfo.IsSymlink() {
		return fmt.Errorf("%w: source %s", ErrSymlink, sourceSafe)
	}
	if !sourceInfo.IsRegular() {
		return fmt.Errorf("source %q is not a regular file", sourceSafe)
	}

	if destinationInfo, statErr := lstatAt(destinationFD, destinationName, destinationSafe); statErr == nil {
		if destinationInfo.IsSymlink() {
			return fmt.Errorf("%w: destination %s", ErrSymlink, destinationSafe)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}

	var renameErr error
	if overwrite {
		renameErr = unix.Renameat(sourceFD, sourceName, destinationFD, destinationName)
	} else {
		renameErr = unix.Renameat2(sourceFD, sourceName, destinationFD, destinationName, unix.RENAME_NOREPLACE)
		if errors.Is(renameErr, unix.EEXIST) {
			return fmt.Errorf("%w: %s", ErrConflict, destinationSafe)
		}
	}
	if renameErr != nil {
		return wrapPathError("renameat", destinationSafe, renameErr)
	}
	return nil
}

func (r *Root) rootDescriptor(create bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return -1, ErrClosed
	}
	if r.fd < 0 {
		fd, err := openAbsoluteDirectory(r.path, create)
		if err != nil {
			return -1, wrapPathError("open root", r.path, err)
		}
		r.fd = fd
	}
	fd, err := unix.FcntlInt(uintptr(r.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("duplicate root descriptor: %w", err)
	}
	return fd, nil
}

func openAbsoluteDirectory(path string, create bool) (int, error) {
	current, err := openDirectoryAt(unix.AT_FDCWD, string(filepath.Separator))
	if err != nil {
		return -1, err
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." {
			continue
		}
		child, openErr := openDirectoryAt(current, component)
		if errors.Is(openErr, unix.ENOENT) && create {
			mkdirErr := unix.Mkdirat(current, component, 0o750)
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(current)
				return -1, mkdirErr
			}
			child, openErr = openDirectoryAt(current, component)
		}
		if openErr != nil {
			_ = unix.Close(current)
			return -1, openErr
		}
		_ = unix.Close(current)
		current = child
	}
	return current, nil
}

func openDirectoryAt(parentFD int, component string) (int, error) {
	fd, err := unix.Openat(parentFD, component, directoryOpenFlags, 0)
	if errors.Is(err, unix.ENOTDIR) {
		// Linux reports ENOTDIR rather than ELOOP for O_NOFOLLOW|O_DIRECTORY
		// on some symlink substitutions. Classify the already-failed lookup
		// without making it part of any later operation.
		var stat unix.Stat_t
		if statErr := unix.Fstatat(parentFD, component, &stat, unix.AT_SYMLINK_NOFOLLOW); statErr == nil && stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return -1, unix.ELOOP
		}
	}
	return fd, err
}

func lstatAt(parentFD int, name, relative string) (EntryInfo, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return EntryInfo{}, wrapPathError("fstatat", relative, err)
	}
	mode := os.FileMode(stat.Mode & 0o777)
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFREG:
	default:
		mode |= os.ModeIrregular
	}
	return EntryInfo{Mode: mode, Size: stat.Size}, nil
}

func removeEntryAt(parentFD int, name, relative string) error {
	info, err := lstatAt(parentFD, name, relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsSymlink() {
		return fmt.Errorf("%w: %s", ErrSymlink, relative)
	}
	if !info.IsDir() {
		if err := unix.Unlinkat(parentFD, name, 0); err != nil {
			return wrapPathError("unlinkat", relative, err)
		}
		return nil
	}

	directoryFD, err := openDirectoryAt(parentFD, name)
	if err != nil {
		return wrapPathError("openat", relative, err)
	}
	directory := os.NewFile(uintptr(directoryFD), relative)
	if directory == nil {
		_ = unix.Close(directoryFD)
		return fmt.Errorf("openat %q: create directory handle", relative)
	}
	names, readErr := directory.Readdirnames(-1)
	if readErr != nil {
		_ = directory.Close()
		return fmt.Errorf("read directory %q: %w", relative, readErr)
	}
	for _, childName := range names {
		childRelative := filepath.Join(relative, childName)
		if err := removeEntryAt(directoryFD, childName, childRelative); err != nil {
			_ = directory.Close()
			return err
		}
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close directory %q: %w", relative, err)
	}
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil {
		return wrapPathError("unlinkat", relative, err)
	}
	return nil
}

func splitRelative(relative string) (parent, name string) {
	parent = filepath.Dir(relative)
	if parent == "." {
		parent = ""
	}
	return parent, filepath.Base(relative)
}

func wrapPathError(op, path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%w: %s", ErrSymlink, path)
	}
	if errors.Is(err, unix.ENOENT) {
		return &os.PathError{Op: op, Path: path, Err: os.ErrNotExist}
	}
	return &os.PathError{Op: op, Path: path, Err: err}
}
