package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/mega"
)

// TestLivePublicMegaCompatibility is intentionally opt-in. The URL is
// supplied by a maintainer-owned fixture and is never printed by this test.
func TestLivePublicMegaCompatibility(t *testing.T) {
	rawURL := os.Getenv("MEGADW_LIVE_MEGA_URL")
	if rawURL == "" {
		t.Skip("MEGADW_LIVE_MEGA_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	job, err := mega.NewClient(nil, "").ResolveLink(ctx, rawURL, "")
	if err != nil {
		t.Fatalf("live public-link compatibility failed: %v", err)
	}
	if len(job.Files) == 0 || job.TotalBytes < 0 {
		t.Fatalf("live public-link compatibility returned invalid metadata")
	}
}
