package download

import (
	"context"
	"fmt"
	"time"

	"github.com/lorenzocorallo/megadw/internal/store"
)

// CheckpointManager owns the ordering rule that makes a resume bitmap
// trustworthy: sync the sparse file first, then commit the corresponding
// bitmap and byte count in SQLite.
type CheckpointManager struct {
	DB       *store.DB
	Interval time.Duration
	Bytes    int64
	Now      func() time.Time
}

type Checkpoint struct {
	File            *PartialFile
	FileID          string
	CompletedBitmap Bitmap
	BytesCommitted  int64
	State           FileState
	UpdatedAt       time.Time
}

func NewCheckpointManager(db *store.DB, interval time.Duration, bytes int64, now func() time.Time) *CheckpointManager {
	if now == nil {
		now = time.Now
	}
	return &CheckpointManager{DB: db, Interval: interval, Bytes: bytes, Now: now}
}

func (c *CheckpointManager) ShouldCheckpoint(last time.Time, bytesSince int64) bool {
	if c == nil {
		return false
	}
	now := c.Now()
	return bytesSince >= c.Bytes || now.Sub(last) >= c.Interval
}

// Commit performs file.Sync before the SQL transaction. If the transfer
// context is already cancelled, the short persistence timeout still allows a
// graceful shutdown to durably record work that was already written.
func (c *CheckpointManager) Commit(ctx context.Context, checkpoint Checkpoint) error {
	if c == nil || c.DB == nil {
		return fmt.Errorf("checkpoint manager is unavailable")
	}
	if checkpoint.File == nil || checkpoint.FileID == "" {
		return fmt.Errorf("checkpoint file and file id are required")
	}
	// Capture the bitmap before Sync. A parallel file scheduler may continue
	// completing ranges while the filesystem flushes; persisting a later,
	// unsynchronized bitmap would make restart trust data that was not part of
	// the durability barrier.
	bitmap := checkpoint.CompletedBitmap.Clone()
	if err := checkpoint.File.Sync(); err != nil {
		return fmt.Errorf("sync partial file: %w", err)
	}
	persistCtx := ctx
	cancelPersist := func() {}
	if ctx == nil || ctx.Err() != nil {
		persistCtx, cancelPersist = context.WithTimeout(context.Background(), 10*time.Second)
	}
	defer cancelPersist()
	state := string(checkpoint.State)
	if state == "" {
		state = string(FileDownloading)
	}
	when := checkpoint.UpdatedAt
	if when.IsZero() {
		when = c.Now()
	}
	if err := c.DB.CheckpointDownloadFiles(persistCtx, []store.DownloadFileCheckpoint{
		{
			FileID:          checkpoint.FileID,
			CompletedBitmap: bitmap,
			BytesCommitted:  checkpoint.BytesCommitted,
			State:           state,
			UpdatedAt:       when,
		},
	}); err != nil {
		return err
	}
	return nil
}
