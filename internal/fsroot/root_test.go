package fsroot_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/fsroot"
)

func TestSanitizeRelativePathRejectsTraversalAndSeparators(t *testing.T) {
	for _, value := range []string{"../outside", "a/../../outside", "/absolute", `a\\b`, "a/./b", "a/../b", "bad\x00name"} {
		if _, err := fsroot.SanitizeRelativePath(value); err == nil {
			t.Errorf("SanitizeRelativePath(%q) accepted an unsafe path", value)
		}
	}
	if got, err := fsroot.SanitizeRelativePath("Unicode/名前.txt"); err != nil || got != filepath.Join("Unicode", "名前.txt") {
		t.Fatalf("safe path = %q, error = %v", got, err)
	}
}

func TestRootRejectsSymlinkComponents(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "root")
	root, err := fsroot.New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Prepare("nested/file.bin"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(rootPath, "escape")); err != nil {
		t.Fatal(err)
	}
	_, err = root.Prepare("escape/file.bin")
	if !errors.Is(err, fsroot.ErrSymlink) {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestRootRejectsSymlinkAtRootAndFinalTarget(t *testing.T) {
	outside := t.TempDir()
	rootPath := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(outside, rootPath); err != nil {
		t.Fatal(err)
	}
	root, err := fsroot.New(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Prepare("file.bin"); !errors.Is(err, fsroot.ErrSymlink) {
		t.Fatalf("symlinked root error = %v, want ErrSymlink", err)
	}

	cleanRoot, err := fsroot.New(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cleanRoot.Prepare("file.bin"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside.bin"), filepath.Join(cleanRoot.Path(), "file.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanRoot.Prepare("file.bin"); !errors.Is(err, fsroot.ErrSymlink) {
		t.Fatalf("symlinked final target error = %v, want ErrSymlink", err)
	}
}

func TestConflictRenamePreservesExtension(t *testing.T) {
	root, err := fsroot.New(filepath.Join(t.TempDir(), "root"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := root.ResolveConflict("folder/file.tar.gz", fsroot.ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("one"), 0o640); err != nil {
		t.Fatal(err)
	}
	second, err := root.ResolveConflict("folder/file.tar.gz", fsroot.ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second) != "file.tar (1).gz" {
		t.Fatalf("renamed path = %q", second)
	}
	if _, err := root.ResolveConflict("folder/file.tar.gz", fsroot.ConflictFail); !errors.Is(err, fsroot.ErrConflict) {
		t.Fatalf("fail policy error = %v", err)
	}
}
