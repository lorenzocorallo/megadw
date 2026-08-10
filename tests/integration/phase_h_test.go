package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/download"
)

func TestPhaseHGracefulShutdownBoundsActiveTransferAndPreservesResumeState(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{
		PayloadSize: 32 << 20,
		Delay:       20 * time.Millisecond,
	})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManagerWithCheckpoint(t, fixture, 4, 1, 4, 100*time.Millisecond)
	jobID := "phase-h-graceful-shutdown"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 1<<20)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJob(jobID); err != nil {
		t.Fatal(err)
	}
	waitForJobState(t, db, jobID, download.JobDownloading)

	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := manager.CloseContext(shutdownContext); err != nil {
		t.Fatalf("graceful manager shutdown: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("graceful shutdown exceeded its bounded test context: %s", elapsed)
	}

	job, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != string(download.JobPaused) {
		t.Fatalf("shutdown job state = %q, want paused", job.State)
	}
	if job.Files[0].State != string(download.FilePaused) {
		t.Fatalf("shutdown file state = %q, want paused", job.Files[0].State)
	}
	partialDirectory := filepath.Join(roots.incomplete, jobID)
	if _, err := os.Stat(partialDirectory); err != nil {
		t.Fatalf("shutdown removed resumable partial directory: %v", err)
	}
}
