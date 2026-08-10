package download

import (
	"context"
	"errors"
	"testing"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

func TestVerifyPartialFileContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := VerifyPartialFileContext(ctx, nil, 1, mega.FileKey{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("verification error = %v, want context cancellation", err)
	}
}
