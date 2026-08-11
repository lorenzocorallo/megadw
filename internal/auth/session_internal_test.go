package auth

import (
	"context"
	"errors"
	"testing"
)

func TestPasswordWorkIsBoundedAndContextAware(t *testing.T) {
	if err := acquirePasswordWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := acquirePasswordWork(context.Background()); err != nil {
		releasePasswordWork()
		t.Fatal(err)
	}
	defer releasePasswordWork()
	defer releasePasswordWork()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acquirePasswordWork(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("third password worker error = %v, want context cancellation", err)
	}
}
