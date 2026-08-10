// Package buildinfo contains the release metadata compiled into megad.
//
// The defaults deliberately identify development builds. Release builds set
// these variables with -ldflags so the binary and its version endpoint can be
// traced back to an exact source revision without requiring files beside the
// executable.
package buildinfo

import "strings"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Metadata is the public, JSON-safe build identity used by the API and logs.
type Metadata struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
}

// Current returns normalized metadata. Empty linker values are treated as
// unknown so a partially populated release command cannot expose ambiguous
// empty fields to operators.
func Current() Metadata {
	return Metadata{
		Version:   nonEmpty(Version, "dev"),
		Commit:    nonEmpty(Commit, "unknown"),
		BuildTime: nonEmpty(BuildTime, "unknown"),
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
