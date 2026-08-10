//go:build linux

package fsroot

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSanitizeRelativePathRejectsTraversalAndSeparators(t *testing.T) {
	for _, value := range []string{"../outside", "a/../../outside", "/absolute", `a\\b`, "a/./b", "a/../b", "bad\x00name"} {
		if _, err := SanitizeRelativePath(value); err == nil {
			t.Errorf("SanitizeRelativePath(%q) accepted an unsafe path", value)
		}
	}
	if got, err := SanitizeRelativePath("Unicode/名前.txt"); err != nil || got != filepath.Join("Unicode", "名前.txt") {
		t.Fatalf("safe path = %q, error = %v", got, err)
	}
}

func TestRootRejectsSymlinkComponents(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "root")
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("nested", 0o750); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(rootPath, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.OpenFile("escape/file.bin", os.O_RDWR|os.O_CREATE, 0o600); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestRootRejectsSymlinkAtRootAndFinalTarget(t *testing.T) {
	outside := t.TempDir()
	rootPath := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(outside, rootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := New(rootPath); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked root error = %v, want ErrSymlink", err)
	}

	cleanRoot, err := New(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanRoot.Close()
	if err := cleanRoot.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside.bin"), filepath.Join(cleanRoot.Path(), "file.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanRoot.OpenFile("file.bin", os.O_RDWR|os.O_CREATE, 0o600); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlinked final target error = %v, want ErrSymlink", err)
	}
}

func TestConflictRenamePreservesExtension(t *testing.T) {
	root, err := New(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	first, err := root.ResolveConflict("folder/file.tar.gz", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	file, _, err := root.OpenOrCreateFile(first, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("one")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := root.ResolveConflict("folder/file.tar.gz", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second) != "file.tar (1).gz" {
		t.Fatalf("renamed path = %q", second)
	}
	if _, err := root.ResolveConflict("folder/file.tar.gz", ConflictFail); !errors.Is(err, ErrConflict) {
		t.Fatalf("fail policy error = %v", err)
	}
}

func TestNormalNestedOpenAndResume(t *testing.T) {
	root, err := New(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, existed, err := root.OpenOrCreateFile("job/nested/file.bin", 0o600)
	if err != nil || existed {
		t.Fatalf("first open file=%v existed=%v", err, existed)
	}
	if _, err := file.Write([]byte("inside")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file, existed, err = root.OpenOrCreateFile("job/nested/file.bin", 0o600)
	if err != nil || !existed {
		t.Fatalf("resume open file=%v existed=%v", err, existed)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(data) != "inside" {
		t.Fatalf("resume data=%q err=%v", data, err)
	}
}

func TestPartialCreationCannotFollowParentSymlinkAfterAnchoring(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("job/parent", 0o750); err != nil {
		t.Fatal(err)
	}
	file, existed, err := root.openFileWithHook("job/parent/partial.mega.part", os.O_RDWR|os.O_CREATE, 0o600, func() {
		held := filepath.Join(rootPath, "job", "parent-held")
		if err := os.Rename(filepath.Join(rootPath, "job", "parent"), held); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(rootPath, "job", "parent")); err != nil {
			t.Fatal(err)
		}
	}, false)
	if err != nil || existed {
		t.Fatalf("anchored partial create file=%v existed=%v", err, existed)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "partial.mega.part")); !os.IsNotExist(err) {
		t.Fatalf("outside partial was created, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "job", "parent-held", "partial.mega.part")); err != nil {
		t.Fatalf("anchored partial was not created inside root: %v", err)
	}
}

func TestResumeCannotOpenOutsideFileAfterParentSymlinkSubstitution(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "resume.bin"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("job/parent", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "job", "parent", "resume.bin"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, _, err := root.openFileWithHook("job/parent/resume.bin", os.O_RDONLY, 0, func() {
		held := filepath.Join(rootPath, "job", "parent-held")
		if err := os.Rename(filepath.Join(rootPath, "job", "parent"), held); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(rootPath, "job", "parent")); err != nil {
			t.Fatal(err)
		}
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(data) != "inside" {
		t.Fatalf("resume opened wrong file data=%q err=%v", data, err)
	}
}

func TestFinalRenameCannotFollowDestinationParentSymlinkAfterAnchoring(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "incomplete")
	destinationPath := filepath.Join(t.TempDir(), "complete")
	outside := t.TempDir()
	outsideSource := t.TempDir()
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideSource, 0o750); err != nil {
		t.Fatal(err)
	}
	source, err := New(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := New(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err := source.MkdirAll("job", 0o750); err != nil {
		t.Fatal(err)
	}
	file, _, err := source.OpenOrCreateFile("job/file.mega.part", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("payload")); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if err := destination.MkdirAll("nested", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := destination.renameFromWithHook(source, "job/file.mega.part", "nested/file.bin", false, func() {
		heldSource := filepath.Join(sourcePath, "job-held")
		if err := os.Rename(filepath.Join(sourcePath, "job"), heldSource); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideSource, filepath.Join(sourcePath, "job")); err != nil {
			t.Fatal(err)
		}
		held := filepath.Join(destinationPath, "nested-held")
		if err := os.Rename(filepath.Join(destinationPath, "nested"), held); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(destinationPath, "nested")); err != nil {
			t.Fatal(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "file.bin")); !os.IsNotExist(err) {
		t.Fatalf("outside final target was created, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(destinationPath, "nested-held", "file.bin")); err != nil {
		t.Fatalf("anchored final target was not created inside root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outsideSource, "file.mega.part")); !os.IsNotExist(err) {
		t.Fatalf("outside source unexpectedly participated in rename, stat error=%v", err)
	}
}

func TestDeletionCannotFollowParentSymlinkAfterAnchoring(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "victim.bin"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("job", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, "job", "victim.bin"), []byte("remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.removeWithHook("job/victim.bin", func() {
		held := filepath.Join(rootPath, "job-held")
		if err := os.Rename(filepath.Join(rootPath, "job"), held); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(rootPath, "job")); err != nil {
			t.Fatal(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outside, "victim.bin"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside victim changed data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "job-held", "victim.bin")); !os.IsNotExist(err) {
		t.Fatalf("anchored victim was not removed, stat error=%v", err)
	}
}

func TestRecursiveCleanupRejectsSymlinkChildren(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "victim.bin"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("job/nested", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "job", "nested", "escape")); err != nil {
		t.Fatal(err)
	}
	if err := root.RemoveAll("job"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("recursive symlink error = %v, want ErrSymlink", err)
	}
	data, err := os.ReadFile(filepath.Join(outside, "victim.bin"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("recursive cleanup changed outside target data=%q err=%v", data, err)
	}
}

func TestRenameNoReplaceIsAtomicConflict(t *testing.T) {
	base := t.TempDir()
	source, err := New(filepath.Join(base, "incomplete"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := New(filepath.Join(base, "complete"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	sourceFile, _, err := source.OpenOrCreateFile("job/part", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceFile.Write([]byte("source")); err != nil {
		sourceFile.Close()
		t.Fatal(err)
	}
	sourceFile.Close()
	destinationFile, _, err := destination.OpenOrCreateFile("file.bin", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	destinationFile.Close()
	if err := destination.RenameFrom(source, "job/part", "file.bin", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("no-replace error = %v, want ErrConflict", err)
	}
	if _, err := source.Lstat("job/part"); err != nil {
		t.Fatalf("source disappeared after conflict: %v", err)
	}
}

func TestRenameFromSurfacesEXDEV(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("/dev/shm is unavailable")
	}
	destinationPath, err := os.MkdirTemp("/dev/shm", "megadw-fsroot-")
	if err != nil {
		t.Skipf("cannot create tmpfs test root: %v", err)
	}
	defer os.RemoveAll(destinationPath)
	source, err := New(filepath.Join(t.TempDir(), "incomplete"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := New(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	file, _, err := source.OpenOrCreateFile("job/part", 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if err := destination.RenameFrom(source, "job/part", "file.bin", false); !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("cross-device rename error = %v, want EXDEV", err)
	}
}
