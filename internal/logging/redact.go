// Package logging contains the small set of safe logging helpers shared by
// the process entry point and release tests. Secrets never belong in normal
// logs; public MEGA link fragments are decryption keys and are treated as
// secrets too.
package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"strings"
)

const redactedValue = "[REDACTED]"

// NewTextLogger returns the production text logger. Attribute values that
// commonly contain credentials are replaced, and URL-like strings have their
// fragment removed before they reach journald/stdout.
func NewTextLogger(writer io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{
		ReplaceAttr: replaceAttr,
	}))
}

// RedactMEGALink retains only the non-secret public-link identifier. The
// fragment is never returned because it contains the file/folder key.
func RedactMEGALink(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Fragment == "" {
		return raw
	}
	parsed.Fragment = redactedValue
	return parsed.String()
}

// SourceHash returns a short stable correlation identifier without exposing a
// URL or its key. It is suitable for a slog field such as source_hash.
func SourceHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:6])
}

// RedactText removes credential-bearing URL fragments from arbitrary text.
// It is intentionally conservative: only strings that parse as URLs with a
// fragment are changed, while ordinary error messages remain readable.
func RedactText(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return value
	}
	for index, field := range fields {
		if parsed, err := url.Parse(strings.Trim(field, "()[]{}<>,;")); err == nil && parsed.Fragment != "" && parsed.Scheme != "" {
			fields[index] = RedactMEGALink(field)
		}
	}
	return strings.Join(fields, " ")
}

func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	key := strings.ToLower(attr.Key)
	if key == "password" || key == "secret" || key == "token" || key == "session" || key == "credential" || key == "link_key" || key == "key" || strings.HasSuffix(key, "_ciphertext") {
		return slog.String(attr.Key, redactedValue)
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, RedactText(attr.Value.String()))
	}
	if attr.Value.Kind() == slog.KindAny {
		if value, ok := attr.Value.Any().(error); ok {
			return slog.String(attr.Key, RedactText(value.Error()))
		}
	}
	return attr
}
