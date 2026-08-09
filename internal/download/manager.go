package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/fsroot"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	defaultCheckpointInterval = 2 * time.Second
	defaultCheckpointBytes    = 256 << 20
	maxQueuedJobs             = 128
)

// Config wires the phase-D single-worker core to persistence and the already
// validated MEGA public-link client.
type Config struct {
	DB       *store.DB
	Secrets  *store.SecretStore
	Mega     *mega.Client
	Settings *settings.Service

	CheckpointInterval time.Duration
	CheckpointBytes    int64
	ConflictPolicy     string
	Now                func() time.Time
}

// Manager owns the bounded job queue. Phase D deliberately runs one job/file
// transfer at a time; later phases can add a scheduler without changing the
// durable file or worker contracts.
type Manager struct {
	db         *store.DB
	secrets    *store.SecretStore
	mega       *mega.Client
	settings   *settings.Service
	worker     *RangeWorker
	checkpoint *CheckpointManager

	checkpointInterval time.Duration
	checkpointBytes    int64
	conflictPolicy     string
	now                func() time.Time

	mu      sync.Mutex
	started bool
	closed  bool
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan string
	pending map[string]struct{}
	active  map[string]struct{}
	done    chan struct{}
	finalMu sync.Mutex
}

func NewManager(config Config) (*Manager, error) {
	if config.DB == nil {
		return nil, fmt.Errorf("download database is required")
	}
	if config.Secrets == nil {
		return nil, fmt.Errorf("download secret store is required")
	}
	if config.Mega == nil {
		return nil, fmt.Errorf("MEGA client is required")
	}
	if config.Settings != nil && (config.CheckpointInterval == 0 || config.CheckpointBytes == 0) {
		value, err := config.Settings.Get(context.Background())
		if err != nil {
			return nil, fmt.Errorf("read download settings: %w", err)
		}
		if config.CheckpointInterval == 0 {
			config.CheckpointInterval = time.Duration(value.Downloads.CheckpointIntervalMs) * time.Millisecond
		}
		if config.CheckpointBytes == 0 {
			config.CheckpointBytes = value.Downloads.CheckpointBytes
		}
	}
	if config.CheckpointInterval == 0 {
		config.CheckpointInterval = defaultCheckpointInterval
	}
	if config.CheckpointInterval < 100*time.Millisecond || config.CheckpointInterval > time.Minute {
		return nil, fmt.Errorf("checkpoint interval is outside the safe range")
	}
	if config.CheckpointBytes == 0 {
		config.CheckpointBytes = defaultCheckpointBytes
	}
	if config.CheckpointBytes < 1<<20 {
		return nil, fmt.Errorf("checkpoint byte threshold is outside the safe range")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Manager{
		db:                 config.DB,
		secrets:            config.Secrets,
		mega:               config.Mega,
		settings:           config.Settings,
		worker:             NewRangeWorker(config.Mega),
		checkpoint:         NewCheckpointManager(config.DB, config.CheckpointInterval, config.CheckpointBytes, config.Now),
		checkpointInterval: config.CheckpointInterval,
		checkpointBytes:    config.CheckpointBytes,
		conflictPolicy:     config.ConflictPolicy,
		now:                config.Now,
		pending:            make(map[string]struct{}),
		active:             make(map[string]struct{}),
	}, nil
}

// Start recovers in-flight rows and begins the bounded queue loop. queued jobs
// are always eligible; ready jobs require an explicit StartJob call. A
// paused_recovery job follows the persisted auto-start setting.
func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("download manager is closed")
	}
	if m.started {
		m.mu.Unlock()
		return nil
	}
	managerCtx, cancel := context.WithCancel(ctx)
	m.ctx, m.cancel, m.queue, m.done, m.started = managerCtx, cancel, make(chan string, 128), make(chan struct{}), true
	m.mu.Unlock()

	if err := m.Recover(ctx); err != nil {
		m.cancel()
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	jobs, err := m.db.ListDownloadJobs(ctx)
	if err != nil {
		m.cancel()
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	go m.runQueue()
	// Requests made before Start are retained in pending. Move them to the
	// bounded queue now that the queue loop exists.
	m.mu.Lock()
	initialPending := make([]string, 0, len(m.pending))
	for jobID := range m.pending {
		initialPending = append(initialPending, jobID)
	}
	for _, jobID := range initialPending {
		select {
		case m.queue <- jobID:
		default:
			m.mu.Unlock()
			return fmt.Errorf("download queue is full during startup")
		}
	}
	m.mu.Unlock()

	autoStart := true
	if m.settings != nil {
		if value, settingsErr := m.settings.Get(ctx); settingsErr == nil {
			autoStart = value.Downloads.AutoStart
		}
	}
	for _, job := range jobs {
		if job.State == string(JobQueued) || (job.State == string(JobPausedRecovery) && autoStart) {
			if err := m.StartJob(job.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Recover is idempotent and leaves all durable completion bits unchanged.
func (m *Manager) Recover(ctx context.Context) error {
	return m.db.MarkDownloadsForRecovery(ctx, m.now())
}

// StartJob queues one persisted job. The queue is bounded; duplicate requests
// are collapsed before they can create duplicate workers.
func (m *Manager) StartJob(jobID string) error {
	if jobID == "" {
		return fmt.Errorf("download job id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("download manager is closed")
	}
	if _, ok := m.pending[jobID]; ok {
		return nil
	}
	if _, ok := m.active[jobID]; ok {
		return nil
	}
	if !m.started {
		if len(m.pending) >= maxQueuedJobs {
			return fmt.Errorf("download queue is full")
		}
		m.pending[jobID] = struct{}{}
		return nil
	}
	select {
	case m.queue <- jobID:
		m.pending[jobID] = struct{}{}
		return nil
	default:
		return fmt.Errorf("download queue is full")
	}
}

func (m *Manager) runQueue() {
	defer close(m.done)
	for {
		select {
		case <-m.ctx.Done():
			return
		case jobID := <-m.queue:
			m.mu.Lock()
			delete(m.pending, jobID)
			if _, alreadyActive := m.active[jobID]; alreadyActive {
				m.mu.Unlock()
				continue
			}
			m.active[jobID] = struct{}{}
			m.mu.Unlock()
			_ = m.runJob(m.ctx, jobID)
			m.mu.Lock()
			delete(m.active, jobID)
			m.mu.Unlock()
		}
	}
}

// RunJob executes a job synchronously. It is useful for deterministic
// integration tests and for callers that do not want to start the queue loop.
func (m *Manager) RunJob(ctx context.Context, jobID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return m.runJob(ctx, jobID)
}

// Close cancels the active request and waits until its checkpoint/pause path
// has completed. No transfer goroutine is left behind.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	started := m.started
	cancel := m.cancel
	done := m.done
	m.mu.Unlock()
	if !started {
		return nil
	}
	cancel()
	<-done
	return nil
}

func (m *Manager) runJob(ctx context.Context, jobID string) error {
	job, err := m.db.GetDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	jobState := JobState(job.State)
	if IsTerminalJobState(jobState) {
		return nil
	}
	if err := TransitionJobState(jobState, JobDownloading); err != nil {
		return err
	}
	if err := m.db.SetDownloadJobState(ctx, job.ID, string(JobDownloading), m.now()); err != nil {
		return err
	}
	job.State = string(JobDownloading)

	for index := range job.Files {
		file := job.Files[index]
		if FileState(file.State) == FileCompleted {
			continue
		}
		if ctx.Err() != nil {
			m.pauseJob(context.Background(), job)
			return ctx.Err()
		}
		if err := TransitionFileState(FileState(file.State), FileDownloading); err != nil {
			return m.failJob(ctx, &job, &file, err)
		}
		if err := m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileDownloading), UpdatedAt: m.now()}); err != nil {
			return err
		}
		job.Files[index].State = string(FileDownloading)
		if err := m.processFile(ctx, &job, &file); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				m.pauseJob(context.Background(), job)
				return err
			}
			return m.failJob(ctx, &job, &file, err)
		}
		job.Files[index].State = string(FileCompleted)
	}
	if ctx.Err() != nil {
		m.pauseJob(context.Background(), job)
		return ctx.Err()
	}
	if err := TransitionJobState(JobState(job.State), JobFinalizing); err != nil {
		return err
	}
	if err := m.db.SetDownloadJobState(ctx, job.ID, string(JobFinalizing), m.now()); err != nil {
		return err
	}
	job.State = string(JobFinalizing)
	if err := TransitionJobState(JobState(job.State), JobCompleted); err != nil {
		return err
	}
	if err := m.db.SetDownloadJobState(ctx, job.ID, string(JobCompleted), m.now()); err != nil {
		return err
	}
	_ = m.db.AddDownloadEvent(ctx, job.ID, "", "completed", "download completed", m.now())
	return nil
}

func (m *Manager) processFile(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord) error {
	planner, err := NewSegmentPlanner(file.SizeBytes, file.SegmentSizeBytes)
	if err != nil {
		return err
	}
	if planner.Count != file.SegmentCount {
		return fmt.Errorf("file %q segment count %d does not match planned count %d", file.ID, file.SegmentCount, planner.Count)
	}
	bitmap, err := bitmapForFile(file, planner.Count)
	if err != nil {
		return err
	}
	committed := committedBytes(planner, bitmap)
	if committed < 0 || committed > file.SizeBytes {
		return fmt.Errorf("file %q committed bytes are invalid", file.ID)
	}
	rawKey, err := m.secrets.Decrypt(file.FileKeyCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt file key: %w", err)
	}
	key, err := mega.DecodeFileKeyBytes(rawKey)
	if err != nil {
		return err
	}
	payloadBytes, err := m.secrets.Decrypt(file.PayloadURLCiphertext)
	if err != nil {
		return fmt.Errorf("decrypt payload location: %w", err)
	}
	payloadURL := string(payloadBytes)
	if payloadURL == "" {
		return fmt.Errorf("payload location is empty")
	}
	incompleteRoot, err := fsRoot(job.IncompleteRoot)
	if err != nil {
		return err
	}
	partial, existed, err := OpenPartialFile(incompleteRoot, job.ID, file.RemotePath, file.SizeBytes)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = partial.Close()
		}
	}()
	if !existed && bitmap.Count() > 0 {
		bitmap = NewBitmapUnchecked(planner.Count)
		committed = 0
	}

	lastCheckpoint := m.now()
	bytesSinceCheckpoint := int64(0)
	dirty := false
	checkpoint := func() error {
		if err := m.checkpoint.Commit(ctx, Checkpoint{
			File:            partial,
			FileID:          file.ID,
			CompletedBitmap: bitmap,
			BytesCommitted:  committed,
			State:           FileDownloading,
			UpdatedAt:       m.now(),
		}); err != nil {
			return err
		}
		dirty = false
		bytesSinceCheckpoint = 0
		lastCheckpoint = m.now()
		return nil
	}

	for index := int64(0); index < planner.Count; index++ {
		if bitmap.IsSet(index) {
			continue
		}
		if err := ctx.Err(); err != nil {
			if dirty {
				_ = checkpoint()
			}
			return err
		}
		segment, err := planner.Segment(index)
		if err != nil {
			return err
		}
		if _, err := m.worker.DownloadRange(ctx, partial, key, payloadURL, segment, file.SizeBytes); err != nil {
			if dirty {
				if checkpointErr := checkpoint(); checkpointErr != nil {
					return checkpointErr
				}
			}
			return err
		}
		if err := bitmap.Set(index); err != nil {
			return err
		}
		committed += segment.Size()
		bytesSinceCheckpoint += segment.Size()
		dirty = true
		if bytesSinceCheckpoint >= m.checkpointBytes || m.now().Sub(lastCheckpoint) >= m.checkpointInterval {
			if err := checkpoint(); err != nil {
				return err
			}
		}
	}
	if dirty || planner.Count == 0 {
		if err := checkpoint(); err != nil {
			return err
		}
	}
	if err := m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileVerifying), UpdatedAt: m.now()}); err != nil {
		return err
	}
	if err := partial.Close(); err != nil {
		return fmt.Errorf("close partial file before verification: %w", err)
	}
	closed = true
	if err := VerifyPartialFile(partial.Path(), file.SizeBytes, key); err != nil {
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileFailed), LastErrorCode: "integrity_mismatch", LastErrorMessage: err.Error(), UpdatedAt: m.now()})
		_ = m.db.AddDownloadEvent(context.Background(), job.ID, file.ID, "error", "file integrity verification failed", m.now())
		return err
	}
	if err := m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileMoving), UpdatedAt: m.now()}); err != nil {
		return err
	}
	finalRelativePath, err := m.finalize(job, file, partial.Path())
	if err != nil {
		code := "finalize_failed"
		if errors.Is(err, ErrCrossDevice) {
			code = "cross_device_finalize"
		}
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileFailed), LastErrorCode: code, LastErrorMessage: err.Error(), UpdatedAt: m.now()})
		return err
	}
	persistCtx := ctx
	cancelPersist := func() {}
	if ctx.Err() != nil {
		persistCtx, cancelPersist = context.WithTimeout(context.Background(), 10*time.Second)
	}
	defer cancelPersist()
	if err := m.db.CompleteDownloadFile(persistCtx, file.ID, finalRelativePath, m.now()); err != nil {
		return err
	}
	file.State = string(FileCompleted)
	file.FinalRelativePath = finalRelativePath
	return nil
}

func bitmapForFile(file *store.DownloadFileRecord, count int64) (Bitmap, error) {
	if len(file.CompletedBitmap) == 0 && count > 0 {
		return NewBitmap(count)
	}
	bitmap := Bitmap(append([]byte(nil), file.CompletedBitmap...))
	if err := bitmap.Validate(count); err != nil {
		return nil, err
	}
	return bitmap, nil
}

func committedBytes(planner SegmentPlanner, bitmap Bitmap) int64 {
	var committed int64
	for index := int64(0); index < planner.Count; index++ {
		if bitmap.IsSet(index) {
			segment, err := planner.Segment(index)
			if err == nil {
				committed += segment.Size()
			}
		}
	}
	return committed
}

func (m *Manager) failJob(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord, cause error) error {
	_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileFailed), LastErrorCode: "download_failed", LastErrorMessage: cause.Error(), UpdatedAt: m.now()})
	_ = m.db.AddDownloadEvent(context.Background(), job.ID, file.ID, "error", cause.Error(), m.now())
	if TransitionJobState(JobState(job.State), JobFailed) == nil {
		_ = m.db.SetDownloadJobState(context.Background(), job.ID, string(JobFailed), m.now())
	}
	return cause
}

func (m *Manager) pauseJob(ctx context.Context, job store.DownloadJobRecord) {
	for _, file := range job.Files {
		state := FileState(file.State)
		if state == FileDownloading || state == FileVerifying || state == FileMoving {
			_ = m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{FileID: file.ID, State: string(FilePaused), UpdatedAt: m.now()})
		}
	}
	if TransitionJobState(JobState(job.State), JobPaused) == nil {
		_ = m.db.SetDownloadJobState(ctx, job.ID, string(JobPaused), m.now())
	}
}

var ErrCrossDevice = errors.New("incomplete and complete roots must share a filesystem")

func (m *Manager) finalize(job *store.DownloadJobRecord, file *store.DownloadFileRecord, partialPath string) (string, error) {
	completeRoot, err := fsRoot(job.CompleteRoot)
	if err != nil {
		return "", err
	}
	policy := m.conflictPolicy
	if policy == "" && m.settings != nil {
		if value, settingsErr := m.settings.Get(context.Background()); settingsErr == nil {
			policy = value.Downloads.ConflictPolicy
		}
	}
	if policy == "" {
		policy = "rename"
	}
	m.finalMu.Lock()
	defer m.finalMu.Unlock()
	target, err := completeRoot.ResolveConflict(file.FinalRelativePath, policy)
	if err != nil {
		return "", err
	}
	if err := os.Rename(partialPath, target); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return "", fmt.Errorf("%w: cannot atomically rename %q into complete root", ErrCrossDevice, file.FinalRelativePath)
		}
		return "", fmt.Errorf("rename completed partial file: %w", err)
	}
	relative, err := filepath.Rel(completeRoot.Path(), target)
	if err != nil || relative == ".." || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("resolve final relative path")
	}
	return relative, nil
}

func fsRoot(path string) (*fsroot.Root, error) {
	return fsroot.New(path)
}
