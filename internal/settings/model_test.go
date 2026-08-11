package settings

import (
	"path/filepath"
	"testing"
)

func TestDefaultHasNoImplicitTransferRoots(t *testing.T) {
	value := Default()
	if value.Paths.IncompleteRoot != "" || value.Paths.CompleteRoot != "" {
		t.Fatalf("fresh transfer roots = %#v, want both empty", value.Paths)
	}
	if value.Paths.Configured() {
		t.Fatal("fresh settings report configured transfer roots")
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("unconfigured fresh settings are invalid: %v", err)
	}
}

func TestPathsAcceptArbitraryExplicitAbsoluteRoots(t *testing.T) {
	base := t.TempDir()
	value := Default()
	value.Paths = PathsSettings{
		IncompleteRoot: filepath.Join(base, "operator-selected", "partial"),
		CompleteRoot:   filepath.Join(base, "operator-selected", "complete"),
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("explicit roots rejected: %v", err)
	}
	if !value.Paths.Configured() {
		t.Fatal("explicit roots did not become configured")
	}
}

func TestPathsRemainUnconfiguredUntilBothRootsAreSet(t *testing.T) {
	value := Default()
	value.Paths.IncompleteRoot = filepath.Join(t.TempDir(), "partial")
	if value.Paths.Configured() {
		t.Fatal("one configured root enabled transfers")
	}
	if err := value.Validate(); err == nil {
		t.Fatal("settings accepted only one configured transfer root")
	}
}
