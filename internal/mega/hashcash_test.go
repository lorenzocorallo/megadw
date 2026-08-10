package mega

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseHashcashChallengeRejectsMalformedOrOversizedToken(t *testing.T) {
	for _, header := range []string{
		"1:255:fixture:not+url-base64",
		"1:255:fixture:" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", hashcashTokenSlot+1))),
	} {
		if _, ok := parseHashcashChallenge(header); ok {
			t.Fatalf("accepted invalid challenge %q", header)
		}
	}
}

func TestHashcashCancellationDuringWorkIsPrompt(t *testing.T) {
	challenge := hashcashChallenge{
		easiness: 0,
		token:    base64.RawURLEncoding.EncodeToString([]byte("cancellation-test")),
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	started := time.Now()
	_, err := solveHashcash(ctx, challenge, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("hashcash cancellation took %s", elapsed)
	}
}
