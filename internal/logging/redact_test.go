package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/logging"
)

func TestProductionLoggerRedactsMEGALinkFragmentsAndSecrets(t *testing.T) {
	const link = "https://mega.nz/file/fixture#private-decryption-key"
	var output bytes.Buffer
	logger := logging.NewTextLogger(&output)
	logger.Info("resolve", "url", link, "password", "do-not-log", "err", slog.AnyValue(fakeError{message: link}))

	text := output.String()
	for _, secret := range []string{"private-decryption-key", "do-not-log"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log output contains secret %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("redacted marker missing from log output: %s", text)
	}
}

func TestSourceHashIsStableAndDoesNotContainSource(t *testing.T) {
	const link = "https://mega.nz/file/fixture#private-decryption-key"
	if got, want := logging.SourceHash(link), logging.SourceHash(link); got != want {
		t.Fatalf("source hash is not stable: %q != %q", got, want)
	}
	if strings.Contains(logging.SourceHash(link), "private") {
		t.Fatal("source hash contains source text")
	}
}

type fakeError struct{ message string }

func (e fakeError) Error() string { return e.message }
