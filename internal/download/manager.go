package download

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/events"
	"github.com/lorenzocorallo/megadw/internal/fsroot"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/network"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	defaultCheckpointInterval = 2 * time.Second
	defaultCheckpointBytes    = 256 << 20
	defaultWorkersPerFile     = 4
	defaultMaxActiveFiles     = 2
	defaultMaxGlobalWorkers   = 8
	defaultReadIdleTimeout    = 90 * time.Second
	maxQueuedJobs             = 128
	maxSpeedMeters            = 512
	maxCachedAccountClients   = 64
)

// Config wires the transfer engine to persistence and the validated MEGA
// public-link client. The concurrency values are deliberately duplicated from
// settings so tests and embedders can construct a manager without an HTTP API.
type Config struct {
	DB       *store.DB
	Secrets  *store.SecretStore
	Mega     *mega.Client
	Settings *settings.Service

	CheckpointInterval time.Duration
	CheckpointBytes    int64
	ConflictPolicy     string

	WorkersPerFile                 int
	MaxActiveFiles                 int
	MaxGlobalWorkers               int
	GlobalSpeedLimitBytesPerSecond int64
	PerJobDefaultLimitBytesPerSec  int64
	// PerJobDefaultLimitBytesPerSecond is the full settings-compatible spelling
	// retained alongside the shorter field for embedders.
	PerJobDefaultLimitBytesPerSecond int64
	ReadIdleTimeout                  time.Duration
	NormalRetryLimit                 int
	TransportPool                    *network.TransportPool
	Events                           *events.Bus

	Now func() time.Time
}

type queueItem struct {
	jobID    string
	priority int
	sequence uint64
}

type jobQueue []queueItem

func (q jobQueue) Len() int { return len(q) }

func (q jobQueue) Less(left, right int) bool {
	if q[left].priority != q[right].priority {
		return q[left].priority > q[right].priority
	}
	return q[left].sequence < q[right].sequence
}

func (q jobQueue) Swap(left, right int) { q[left], q[right] = q[right], q[left] }

func (q *jobQueue) Push(value any) { *q = append(*q, value.(queueItem)) }

func (q *jobQueue) Pop() any {
	old := *q
	last := len(old) - 1
	value := old[last]
	*q = old[:last]
	return value
}

type jobControl struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	pauseRequested      bool
	queuePauseRequested bool
	cancelRequested     bool
	deletePartialFiles  bool
}

type jobResult struct {
	jobID string
	err   error
}

type quotaTimer struct {
	timer *time.Timer
	done  chan struct{}
	once  sync.Once
}

func (t *quotaTimer) finish() {
	if t != nil {
		t.once.Do(func() { close(t.done) })
	}
}

// Manager owns the bounded priority queue, file slots, and global network
// worker semaphore. At most maxActiveFiles job goroutines are owned by the
// queue dispatcher, and each job has at most maxActiveFiles file workers.
// Actual active files and network workers are constrained by the process-wide
// semaphores, so a large folder cannot create an unbounded goroutine or FD
// population.
type Manager struct {
	db       *store.DB
	secrets  *store.SecretStore
	mega     *mega.Client
	settings *settings.Service
	worker   *RangeWorker

	checkpoint *CheckpointManager

	checkpointInterval time.Duration
	checkpointBytes    int64
	conflictPolicy     string
	now                func() time.Time

	workersPerFile   int
	maxActiveFiles   int
	maxGlobalWorkers int
	fileSlots        chan struct{}
	workerSlots      chan struct{}
	globalLimiter    *BandwidthLimiter
	perJobLimit      int64
	readIdleTimeout  time.Duration
	normalRetryLimit int
	transportPool    *network.TransportPool
	events           *events.Bus

	mu             sync.Mutex
	started        bool
	closed         bool
	ctx            context.Context
	cancel         context.CancelFunc
	queue          jobQueue
	queueWake      chan struct{}
	jobResults     chan jobResult
	pending        map[string]struct{}
	active         map[string]struct{}
	controls       map[string]*jobControl
	pendingPrio    map[string]int
	pendingSeq     map[string]uint64
	queuePaused    bool
	queuePausedIDs map[string]struct{}
	sequence       uint64
	done           chan struct{}
	jobsWG         sync.WaitGroup

	finalMu   sync.Mutex
	accountMu sync.Mutex
	// accountClients contains at most maxCachedAccountClients session-bound,
	// authentication-only clients; it owns no goroutine or transport and is
	// cleared on shutdown or account mutation.
	accountClients map[string]*mega.Client

	speedMu    sync.Mutex
	speeds     map[string]*SpeedMeter
	speedOrder []string

	quotaTimers map[string]*quotaTimer
	quotaAll    map[*quotaTimer]struct{}
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

	if config.Settings != nil {
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
		if config.ConflictPolicy == "" {
			config.ConflictPolicy = value.Downloads.ConflictPolicy
		}
		if config.WorkersPerFile == 0 {
			config.WorkersPerFile = value.Downloads.WorkersPerFile
		}
		if config.MaxActiveFiles == 0 {
			config.MaxActiveFiles = value.Downloads.MaxActiveFiles
		}
		if config.MaxGlobalWorkers == 0 {
			config.MaxGlobalWorkers = value.Downloads.MaxGlobalWorkers
		}
		if config.GlobalSpeedLimitBytesPerSecond == 0 {
			config.GlobalSpeedLimitBytesPerSecond = value.Downloads.GlobalSpeedLimitBytesPerSecond
		}
		if config.PerJobDefaultLimitBytesPerSec == 0 {
			config.PerJobDefaultLimitBytesPerSec = value.Downloads.PerJobDefaultLimitBytesPerSecond
		}
		if config.ReadIdleTimeout == 0 {
			config.ReadIdleTimeout = time.Duration(value.Network.ReadIdleTimeoutSeconds) * time.Second
		}
		if config.NormalRetryLimit == 0 {
			config.NormalRetryLimit = value.Downloads.NormalRetryLimit
		}
	}
	if config.PerJobDefaultLimitBytesPerSec == 0 {
		config.PerJobDefaultLimitBytesPerSec = config.PerJobDefaultLimitBytesPerSecond
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
	if config.WorkersPerFile == 0 {
		config.WorkersPerFile = defaultWorkersPerFile
	}
	if config.MaxActiveFiles == 0 {
		config.MaxActiveFiles = defaultMaxActiveFiles
	}
	if config.MaxGlobalWorkers == 0 {
		config.MaxGlobalWorkers = defaultMaxGlobalWorkers
	}
	if config.WorkersPerFile < 1 || config.WorkersPerFile > 16 {
		return nil, fmt.Errorf("workers per file is outside the safe range")
	}
	if config.MaxActiveFiles < 1 || config.MaxActiveFiles > 16 {
		return nil, fmt.Errorf("active file count is outside the safe range")
	}
	if config.MaxGlobalWorkers < 1 || config.MaxGlobalWorkers > 64 {
		return nil, fmt.Errorf("global worker count is outside the safe range")
	}
	if config.GlobalSpeedLimitBytesPerSecond < 0 || config.PerJobDefaultLimitBytesPerSec < 0 {
		return nil, fmt.Errorf("speed limits must not be negative")
	}
	if config.ReadIdleTimeout == 0 {
		config.ReadIdleTimeout = defaultReadIdleTimeout
	}
	if config.NormalRetryLimit == 0 {
		config.NormalRetryLimit = 5
	}
	if config.NormalRetryLimit < 0 || config.NormalRetryLimit > 20 {
		return nil, fmt.Errorf("normal retry limit is outside the safe range")
	}
	if config.ReadIdleTimeout < time.Second || config.ReadIdleTimeout > time.Hour {
		return nil, fmt.Errorf("read idle timeout is outside the safe range")
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	manager := &Manager{
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
		workersPerFile:     config.WorkersPerFile,
		maxActiveFiles:     config.MaxActiveFiles,
		maxGlobalWorkers:   config.MaxGlobalWorkers,
		fileSlots:          make(chan struct{}, config.MaxActiveFiles),
		workerSlots:        make(chan struct{}, config.MaxGlobalWorkers),
		globalLimiter:      NewBandwidthLimiter(config.GlobalSpeedLimitBytesPerSecond),
		perJobLimit:        config.PerJobDefaultLimitBytesPerSec,
		readIdleTimeout:    config.ReadIdleTimeout,
		normalRetryLimit:   config.NormalRetryLimit,
		transportPool:      config.TransportPool,
		events:             config.Events,
		pending:            make(map[string]struct{}),
		active:             make(map[string]struct{}),
		controls:           make(map[string]*jobControl),
		pendingPrio:        make(map[string]int),
		pendingSeq:         make(map[string]uint64),
		queuePausedIDs:     make(map[string]struct{}),
		speeds:             make(map[string]*SpeedMeter),
		quotaTimers:        make(map[string]*quotaTimer),
		quotaAll:           make(map[*quotaTimer]struct{}),
		accountClients:     make(map[string]*mega.Client),
	}
	heap.Init(&manager.queue)
	return manager, nil
}

// Start recovers in-flight rows and begins the bounded priority queue loop.
// Queued jobs are always eligible; ready jobs require an explicit StartJob
// call. A paused_recovery job follows the persisted auto-start setting.
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
	m.ctx = managerCtx
	m.cancel = cancel
	m.queueWake = make(chan struct{}, 1)
	m.jobResults = make(chan jobResult, m.maxActiveFiles*2)
	m.done = make(chan struct{})
	m.started = true
	m.mu.Unlock()

	if err := m.Recover(ctx); err != nil {
		cancel()
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	jobs, err := m.db.ListDownloadJobs(ctx)
	if err != nil {
		cancel()
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	go m.runQueue()

	// Requests made before Start are retained in pending. Promote them now
	// that the dispatcher and its bounded heap exist.
	m.mu.Lock()
	m.promotePendingLocked()
	m.mu.Unlock()

	autoStart := true
	if m.settings != nil {
		if value, settingsErr := m.settings.Get(ctx); settingsErr == nil {
			autoStart = value.Downloads.AutoStart
		}
	}
	for _, job := range jobs {
		if job.State == string(JobQueued) || (job.State == string(JobPausedRecovery) && autoStart) {
			if err := validateTransferRoots(job); err != nil {
				// A fresh install has no queued jobs, while an upgrade may have
				// jobs with their own persisted roots. Never start a job whose
				// durable roots are missing or invalid.
				if errors.Is(err, ErrTransferRootsNotConfigured) {
					continue
				}
				return err
			}
			if job.State == string(JobPausedRecovery) {
				_ = m.db.SetDownloadJobState(ctx, job.ID, string(JobQueued), m.now())
			}
			if err := m.StartJob(job.ID); err != nil {
				return err
			}
		}
		if job.State == string(JobWaitingQuota) {
			m.schedulePersistedQuotaRetry(job)
		}
	}
	return nil
}

// Recover is idempotent and leaves all durable completion bits unchanged.
func (m *Manager) Recover(ctx context.Context) error {
	return m.db.MarkDownloadsForRecovery(ctx, m.now())
}

// StartJob queues one persisted job at the default priority. Duplicate
// requests are collapsed before they can create duplicate workers.
func (m *Manager) StartJob(jobID string) error {
	return m.StartJobWithPriority(jobID, 0)
}

// StartJobWithPriority queues a job. Larger priorities run first; equal
// priorities retain FIFO order by enqueue sequence.
func (m *Manager) StartJobWithPriority(jobID string, priority int) error {
	if jobID == "" {
		return fmt.Errorf("download job id is required")
	}
	job, err := m.db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		return err
	}
	if err := validateTransferRoots(job); err != nil {
		return err
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
	if len(m.pending) >= maxQueuedJobs {
		return fmt.Errorf("download queue is full")
	}
	m.pending[jobID] = struct{}{}
	m.pendingPrio[jobID] = priority
	m.sequence++
	m.pendingSeq[jobID] = m.sequence
	if m.started {
		heap.Push(&m.queue, queueItem{jobID: jobID, priority: priority, sequence: m.sequence})
		m.compactQueueLocked()
		m.signalLocked()
	}
	return nil
}

// QueueJob is an explicit queue-oriented alias for StartJobWithPriority.
func (m *Manager) QueueJob(jobID string, priority int) error {
	return m.StartJobWithPriority(jobID, priority)
}

func (m *Manager) promotePendingLocked() {
	for jobID := range m.pending {
		heap.Push(&m.queue, queueItem{jobID: jobID, priority: m.pendingPrio[jobID], sequence: m.pendingSeq[jobID]})
	}
	if len(m.pending) > 0 {
		m.signalLocked()
	}
}

func (m *Manager) pushPendingLocked(jobID string, priority int) {
	m.sequence++
	m.pendingPrio[jobID] = priority
	m.pendingSeq[jobID] = m.sequence
	heap.Push(&m.queue, queueItem{jobID: jobID, priority: priority, sequence: m.sequence})
	m.compactQueueLocked()
}

// compactQueueLocked bounds stale heap entries left by pause/resume requests.
// The queue remains a small control structure; payload work never enters it.
func (m *Manager) compactQueueLocked() {
	if m.queue.Len() <= maxQueuedJobs*2 {
		return
	}
	compacted := make(jobQueue, 0, len(m.pending))
	for _, item := range m.queue {
		if sequence, ok := m.pendingSeq[item.jobID]; ok && sequence == item.sequence {
			compacted = append(compacted, item)
		}
	}
	m.queue = compacted
	heap.Init(&m.queue)
}

func (m *Manager) signalLocked() {
	if m.queueWake == nil {
		return
	}
	select {
	case m.queueWake <- struct{}{}:
	default:
	}
}

func (m *Manager) runQueue() {
	defer close(m.done)
	for {
		if m.ctx.Err() != nil {
			m.stopActiveJobs()
			m.jobsWG.Wait()
			m.finishShutdown()
			return
		}
		m.launchQueuedJobs()
		select {
		case <-m.ctx.Done():
			m.stopActiveJobs()
			m.jobsWG.Wait()
			m.finishShutdown()
			return
		case <-m.queueWake:
		case result := <-m.jobResults:
			m.finishJob(result)
		}
	}
}

func (m *Manager) finishShutdown() {
	m.mu.Lock()
	for jobID, control := range m.controls {
		delete(m.active, jobID)
		delete(m.controls, jobID)
		close(control.done)
	}
	m.mu.Unlock()
}

func (m *Manager) launchQueuedJobs() {
	for {
		m.mu.Lock()
		if m.queuePaused || len(m.active) >= m.maxActiveFiles || m.queue.Len() == 0 {
			m.mu.Unlock()
			return
		}
		item := heap.Pop(&m.queue).(queueItem)
		pendingSequence, stillPending := m.pendingSeq[item.jobID]
		if !stillPending || pendingSequence != item.sequence {
			m.mu.Unlock()
			continue
		}
		delete(m.pending, item.jobID)
		delete(m.pendingPrio, item.jobID)
		delete(m.pendingSeq, item.jobID)
		jobCtx, cancel := context.WithCancel(m.ctx)
		control := &jobControl{ctx: jobCtx, cancel: cancel, done: make(chan struct{})}
		m.active[item.jobID] = struct{}{}
		m.controls[item.jobID] = control
		m.jobsWG.Add(1)
		m.mu.Unlock()

		go func(jobID string, control *jobControl) {
			defer m.jobsWG.Done()
			err := m.runJob(control.ctx, jobID)
			m.jobResults <- jobResult{jobID: jobID, err: err}
		}(item.jobID, control)
	}
}

func (m *Manager) stopActiveJobs() {
	m.mu.Lock()
	for _, control := range m.controls {
		control.cancel()
	}
	m.mu.Unlock()
}

func (m *Manager) finishJob(result jobResult) {
	m.mu.Lock()
	control := m.controls[result.jobID]
	if control == nil {
		m.mu.Unlock()
		return
	}
	cancelRequested := control.cancelRequested
	deletePartial := control.deletePartialFiles
	individuallyPaused := control.pauseRequested
	requeue := control.queuePauseRequested && !cancelRequested && !individuallyPaused && !m.queuePaused
	if control.queuePauseRequested && !cancelRequested && !individuallyPaused && m.queuePaused {
		m.queuePausedIDs[result.jobID] = struct{}{}
	}
	m.mu.Unlock()

	if cancelRequested {
		_ = m.cancelPersistedJob(result.jobID, deletePartial)
	}

	m.mu.Lock()
	delete(m.active, result.jobID)
	delete(m.controls, result.jobID)
	if requeue {
		m.pending[result.jobID] = struct{}{}
		m.pushPendingLocked(result.jobID, 0)
	}
	close(control.done)
	m.signalLocked()
	m.mu.Unlock()
}

// RunJob executes a job synchronously. It uses the same per-file and global
// network semaphores as queued jobs, which makes deterministic integration
// tests and embedded callers obey the same resource cap.
func (m *Manager) RunJob(ctx context.Context, jobID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("download manager is closed")
	}
	if _, active := m.active[jobID]; active {
		m.mu.Unlock()
		return fmt.Errorf("job %q is already running", jobID)
	}
	if _, pending := m.pending[jobID]; pending {
		m.mu.Unlock()
		return fmt.Errorf("job %q is already queued", jobID)
	}
	if len(m.active) >= m.maxActiveFiles {
		m.mu.Unlock()
		return fmt.Errorf("active job limit is reached")
	}
	m.active[jobID] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.active, jobID)
		m.signalLocked()
		m.mu.Unlock()
	}()
	return m.runJob(ctx, jobID)
}

// Close cancels active queue jobs and waits until their checkpoint/pause paths
// have completed. No goroutine owned by the manager remains afterwards.
//
// Callers that own a process shutdown deadline should use CloseContext. The
// context is only a bound for waiting; cancellation is always sent to every
// active network request before the wait begins.
func (m *Manager) Close() error {
	return m.CloseContext(context.Background())
}

// CloseContext performs an orderly bounded shutdown of the transfer engine.
// Active jobs observe cancellation, checkpoint already-written ranges, and
// persist a resumable paused state before the manager signals completion.
func (m *Manager) CloseContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
		m.clearAccountClients()
		return nil
	}
	if cancel != nil {
		cancel()
	}
	m.mu.Lock()
	quotaTimers := make([]*quotaTimer, 0, len(m.quotaAll))
	for timer := range m.quotaAll {
		if timer.timer.Stop() {
			timer.finish()
		}
		quotaTimers = append(quotaTimers, timer)
	}
	m.quotaTimers = make(map[string]*quotaTimer)
	m.quotaAll = make(map[*quotaTimer]struct{})
	m.mu.Unlock()
	if done == nil {
		m.clearAccountClients()
		return nil
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	for _, timer := range quotaTimers {
		select {
		case <-timer.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.clearAccountClients()
	return nil
}

// InvalidateAccountSession drops cached session-bound clients after an
// account edit/test/re-authentication. It never closes the shared proxy
// transport; the transport pool owns that resource.
func (m *Manager) InvalidateAccountSession(accountID string) {
	if m == nil || accountID == "" {
		return
	}
	m.accountMu.Lock()
	defer m.accountMu.Unlock()
	prefix := accountID + "\x00"
	for key := range m.accountClients {
		if strings.HasPrefix(key, prefix) {
			delete(m.accountClients, key)
		}
	}
}

// InvalidateProxySessions drops cached account clients that route through a
// changed or deleted proxy profile. The shared transport pool remains the
// owner of the underlying HTTP clients.
func (m *Manager) InvalidateProxySessions(proxyID string) {
	if m == nil || proxyID == "" {
		return
	}
	m.accountMu.Lock()
	defer m.accountMu.Unlock()
	suffix := "\x00" + proxyID
	for key := range m.accountClients {
		if strings.HasSuffix(key, suffix) {
			delete(m.accountClients, key)
		}
	}
}

func (m *Manager) clearAccountClients() {
	if m == nil {
		return
	}
	m.accountMu.Lock()
	m.accountClients = make(map[string]*mega.Client)
	m.accountMu.Unlock()
}

// PauseJob requests a durable pause and waits for active range workers to
// release their file and network slots.
func (m *Manager) PauseJob(ctx context.Context, jobID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if timer := m.quotaTimers[jobID]; timer != nil {
		if timer.timer.Stop() {
			timer.finish()
			delete(m.quotaAll, timer)
		}
		delete(m.quotaTimers, jobID)
	}
	m.mu.Unlock()
	m.mu.Lock()
	delete(m.queuePausedIDs, jobID)
	if control := m.controls[jobID]; control != nil {
		control.pauseRequested = true
		done := control.done
		control.cancel()
		m.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	delete(m.pending, jobID)
	delete(m.pendingPrio, jobID)
	delete(m.pendingSeq, jobID)
	m.compactQueueLocked()
	m.mu.Unlock()

	job, err := m.db.GetDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if IsTerminalJobState(JobState(job.State)) {
		return fmt.Errorf("cannot pause terminal job %q", jobID)
	}
	m.pauseJob(context.Background(), job)
	return nil
}

// Pause is a concise action alias used by non-HTTP callers.
func (m *Manager) Pause(ctx context.Context, jobID string) error {
	return m.PauseJob(ctx, jobID)
}

// ResumeJob requeues a paused, recovered, waiting, or failed job without
// changing its selected account, proxy, or partial file.
func (m *Manager) ResumeJob(ctx context.Context, jobID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if _, active := m.active[jobID]; active {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	job, err := m.db.GetDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if err := validateTransferRoots(job); err != nil {
		return err
	}
	state := JobState(job.State)
	if IsTerminalJobState(state) && state != JobFailed {
		return fmt.Errorf("cannot resume terminal job %q", jobID)
	}
	m.mu.Lock()
	if timer := m.quotaTimers[jobID]; timer != nil {
		if timer.timer.Stop() {
			timer.finish()
			delete(m.quotaAll, timer)
		}
		delete(m.quotaTimers, jobID)
	}
	m.mu.Unlock()
	if err := TransitionJobState(state, JobQueued); err != nil {
		return err
	}
	if err := m.db.ClearDownloadQuotaState(ctx, jobID, string(JobQueued), m.now()); err != nil {
		return err
	}
	_ = m.db.AddDownloadEvent(context.Background(), jobID, "", "state", "download queued for resume", m.now())
	m.publishJobSnapshot(jobID)
	return m.StartJob(jobID)
}

// Resume is a concise action alias used by non-HTTP callers.
func (m *Manager) Resume(ctx context.Context, jobID string) error {
	return m.ResumeJob(ctx, jobID)
}

func quotaDelay(index int) time.Duration {
	switch index {
	case 1:
		return time.Minute
	case 2:
		return 2 * time.Minute
	case 3:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}

func (m *Manager) schedulePersistedQuotaRetry(job store.DownloadJobRecord) {
	delay := time.Minute
	if job.QuotaNextRetryAt != nil {
		delay = time.Until(*job.QuotaNextRetryAt)
		if delay < 0 {
			delay = 0
		}
	}
	m.scheduleQuotaRetry(job.ID, delay)
}

func (m *Manager) scheduleQuotaRetry(jobID string, delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	m.mu.Lock()
	if m.closed || m.ctx == nil || m.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	if old := m.quotaTimers[jobID]; old != nil {
		if old.timer.Stop() {
			old.finish()
			delete(m.quotaAll, old)
		}
	}
	timer := &quotaTimer{done: make(chan struct{})}
	timer.timer = time.AfterFunc(delay, func() {
		defer func() {
			m.mu.Lock()
			if m.quotaTimers[jobID] == timer {
				delete(m.quotaTimers, jobID)
			}
			delete(m.quotaAll, timer)
			m.mu.Unlock()
			timer.finish()
		}()
		m.mu.Lock()
		closed := m.closed || m.ctx == nil || m.ctx.Err() != nil
		m.mu.Unlock()
		if closed {
			return
		}
		_ = m.ResumeJob(context.Background(), jobID)
	})
	m.quotaTimers[jobID] = timer
	m.quotaAll[timer] = struct{}{}
	m.mu.Unlock()
}

func (m *Manager) enterWaitingQuota(job *store.DownloadJobRecord, file *store.DownloadFileRecord, quota *QuotaError) error {
	index := job.QuotaRetryIndex + 1
	delay := quotaDelay(index)
	if quota != nil && quota.RetryAfter > 0 {
		delay = quota.RetryAfter
	}
	next := m.now().Add(delay)
	message := ErrQuota.Error()
	if quota != nil && quota.Cause != nil {
		message = quota.Error()
	}
	if err := m.db.SetDownloadQuotaState(context.Background(), job.ID, string(JobWaitingQuota), "quota_exhausted", message, &next, index, m.now()); err != nil {
		return err
	}
	current, err := m.db.GetDownloadJob(context.Background(), job.ID)
	if err == nil {
		*job = current
	}
	for _, candidate := range job.Files {
		if FileState(candidate.State) == FileCompleted {
			continue
		}
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: candidate.ID, State: string(FileWaiting), LastErrorCode: "quota_exhausted", LastErrorMessage: message, UpdatedAt: m.now()})
	}
	_ = m.db.AddDownloadEvent(context.Background(), job.ID, fileID(file), "warning", "MEGA quota exhausted; download is waiting", m.now())
	m.publishJobSnapshot(job.ID)
	m.scheduleQuotaRetry(job.ID, delay)
	return nil
}

// CancelJob stops a job, persists cancelled states, and optionally removes
// only its job-scoped partial directory.
func (m *Manager) CancelJob(ctx context.Context, jobID string, deletePartialFiles bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if timer := m.quotaTimers[jobID]; timer != nil {
		if timer.timer.Stop() {
			timer.finish()
			delete(m.quotaAll, timer)
		}
		delete(m.quotaTimers, jobID)
	}
	m.mu.Unlock()
	m.mu.Lock()
	if control := m.controls[jobID]; control != nil {
		control.cancelRequested = true
		control.deletePartialFiles = deletePartialFiles
		done := control.done
		control.cancel()
		m.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
		job, err := m.db.GetDownloadJob(ctx, jobID)
		if err != nil {
			return err
		}
		if JobState(job.State) != JobCancelled {
			return fmt.Errorf("job %q was not cancelled", jobID)
		}
		return nil
	}
	delete(m.pending, jobID)
	delete(m.pendingPrio, jobID)
	delete(m.pendingSeq, jobID)
	m.mu.Unlock()
	return m.cancelPersistedJob(jobID, deletePartialFiles)
}

// Cancel is a concise action alias used by non-HTTP callers.
func (m *Manager) Cancel(ctx context.Context, jobID string, deletePartialFiles bool) error {
	return m.CancelJob(ctx, jobID, deletePartialFiles)
}

// DeleteJob removes durable metadata after the caller has made an explicit
// decision about payload files. Unfinished jobs require deleteFiles=true so a
// UI cannot accidentally strand a partial tree with no owning record.
func (m *Manager) DeleteJob(ctx context.Context, jobID string, deleteFiles bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	job, err := m.db.GetDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if !deleteFiles && JobState(job.State) != JobCompleted {
		return fmt.Errorf("deleteFiles must be true for an unfinished download")
	}
	m.mu.Lock()
	_, active := m.active[jobID]
	_, pending := m.pending[jobID]
	m.mu.Unlock()
	if active {
		if err := m.CancelJob(ctx, jobID, deleteFiles); err != nil {
			return err
		}
	} else if pending || !IsTerminalJobState(JobState(job.State)) {
		m.mu.Lock()
		delete(m.pending, jobID)
		delete(m.pendingPrio, jobID)
		delete(m.pendingSeq, jobID)
		m.compactQueueLocked()
		m.mu.Unlock()
		if err := m.cancelPersistedJob(jobID, deleteFiles); err != nil {
			return err
		}
	}
	job, err = m.db.GetDownloadJob(ctx, jobID)
	if err != nil {
		return err
	}
	if deleteFiles {
		if err := m.removePartialDirectory(job); err != nil {
			return err
		}
		if err := removeCompletedFiles(job); err != nil {
			return err
		}
	}
	if err := m.db.DeleteDownloadJob(ctx, jobID); err != nil {
		return err
	}
	if m.events != nil {
		m.events.Publish(events.Event{Name: events.JobUpdated, JobID: jobID, Timestamp: m.now().UTC(), Data: map[string]any{"id": jobID, "deleted": true}})
	}
	return nil
}

func removeCompletedFiles(job store.DownloadJobRecord) error {
	root, err := fsRoot(job.CompleteRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, file := range job.Files {
		if FileState(file.State) != FileCompleted || file.FinalRelativePath == "" {
			continue
		}
		info, statErr := root.Lstat(file.FinalRelativePath)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect completed file: %w", statErr)
		}
		if info.IsSymlink() {
			return fmt.Errorf("refusing to remove symlinked completed file")
		}
		if err := root.Remove(file.FinalRelativePath); err != nil {
			return fmt.Errorf("remove completed file: %w", err)
		}
	}
	return nil
}

func (m *Manager) cancelPersistedJob(jobID string, deletePartialFiles bool) error {
	job, err := m.db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		return err
	}
	if JobState(job.State) == JobCompleted || JobState(job.State) == JobCancelled {
		return fmt.Errorf("cannot cancel terminal job %q", jobID)
	}
	for _, file := range job.Files {
		if FileState(file.State) == FileCompleted {
			continue
		}
		if err := m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{
			FileID: file.ID, State: string(FileCancelled), UpdatedAt: m.now(),
		}); err != nil {
			return err
		}
	}
	if err := m.db.SetDownloadJobState(context.Background(), jobID, string(JobCancelled), m.now()); err != nil {
		return err
	}
	_ = m.db.AddDownloadEvent(context.Background(), jobID, "", "state", "download cancelled", m.now())
	m.publishJobSnapshot(jobID)
	if deletePartialFiles {
		if err := m.removePartialDirectory(job); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) removePartialDirectory(job store.DownloadJobRecord) error {
	root, err := fsRoot(job.IncompleteRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := fsroot.SanitizeComponent(job.ID)
	if err != nil {
		return err
	}
	if err := root.RemoveAll(relative); err != nil {
		return fmt.Errorf("remove partial directory: %w", err)
	}
	return nil
}

// PauseQueue prevents new jobs from starting and asks active jobs to pause.
// Queued jobs retain their FIFO/priority position.
func (m *Manager) PauseQueue() {
	m.mu.Lock()
	m.queuePaused = true
	for _, control := range m.controls {
		control.queuePauseRequested = true
		control.cancel()
	}
	m.signalLocked()
	m.mu.Unlock()
	m.publishQueueUpdate(true)
}

// ResumeQueue allows queued jobs to run and requeues jobs paused by the queue
// action. Jobs paused individually remain paused.
func (m *Manager) ResumeQueue() {
	m.mu.Lock()
	m.queuePaused = false
	for jobID := range m.queuePausedIDs {
		if _, active := m.active[jobID]; !active {
			if _, pending := m.pending[jobID]; !pending {
				m.pending[jobID] = struct{}{}
				m.pushPendingLocked(jobID, 0)
			}
		}
		delete(m.queuePausedIDs, jobID)
	}
	m.signalLocked()
	m.mu.Unlock()
	m.publishQueueUpdate(false)
}

// Speed returns the current bounded rolling speed for a job.
func (m *Manager) Speed(jobID string) SpeedSnapshot {
	m.speedMu.Lock()
	meter := m.speeds[jobID]
	m.speedMu.Unlock()
	if meter == nil {
		return SpeedSnapshot{}
	}
	return meter.Snapshot()
}

// SpeedMeter returns the live meter for embedders that need more than the
// value snapshot. The returned meter remains race-safe.
func (m *Manager) SpeedMeter(jobID string) *SpeedMeter {
	return m.getSpeedMeter(jobID)
}

// publishJobSnapshot emits a current durable snapshot for lifecycle changes.
// It intentionally does not write through the bus synchronously: Bus.Publish
// only updates bounded in-memory subscriber state and never waits for an SSE
// writer.
func (m *Manager) publishJobSnapshot(jobID string) {
	if m.events == nil || jobID == "" {
		return
	}
	job, err := m.db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		return
	}
	speed := m.Speed(jobID)
	remaining := job.TotalBytes - job.BytesCommitted
	eta := int64(0)
	if remaining > 0 && speed.BytesPerSecond > 0 {
		eta = int64(float64(remaining) / speed.BytesPerSecond)
	}
	files := make([]map[string]any, 0, len(job.Files))
	for _, file := range job.Files {
		files = append(files, map[string]any{
			"id":             file.ID,
			"state":          file.State,
			"bytesCommitted": file.BytesCommitted,
			"sizeBytes":      file.SizeBytes,
			"updatedAt":      file.UpdatedAt,
		})
	}
	m.events.Publish(events.Event{
		Name:      events.JobUpdated,
		JobID:     jobID,
		Timestamp: m.now().UTC(),
		Data: map[string]any{
			"id":                  job.ID,
			"state":               job.State,
			"totalBytes":          job.TotalBytes,
			"bytesCommitted":      job.BytesCommitted,
			"speedBytesPerSecond": speed.BytesPerSecond,
			"etaSeconds":          eta,
			"updatedAt":           job.UpdatedAt,
			"quotaNextRetryAt":    job.QuotaNextRetryAt,
			"quotaRetryIndex":     job.QuotaRetryIndex,
			"lastErrorCode":       job.LastErrorCode,
			"lastErrorMessage":    job.LastErrorMessage,
			"files":               files,
		},
	})
}

func (m *Manager) publishProgress(jobID, fileID, state string, committed, size int64) {
	if m.events == nil || jobID == "" {
		return
	}
	speed := m.Speed(jobID)
	m.events.Publish(events.Event{
		Name:      events.SpeedUpdated,
		JobID:     jobID,
		FileID:    fileID,
		Timestamp: m.now().UTC(),
		Data: map[string]any{
			"id":                  jobID,
			"downloadId":          jobID,
			"fileId":              fileID,
			"state":               state,
			"bytesCommitted":      committed,
			"sizeBytes":           size,
			"speedBytesPerSecond": speed.BytesPerSecond,
			"sessionBytes":        speed.TotalBytes,
		},
	})
}

func (m *Manager) publishQueueUpdate(paused bool) {
	if m.events == nil {
		return
	}
	m.events.Publish(events.Event{Name: events.QueueUpdated, Timestamp: m.now().UTC(), Data: map[string]any{"queuePaused": paused}})
}

func (m *Manager) getSpeedMeter(jobID string) *SpeedMeter {
	m.speedMu.Lock()
	defer m.speedMu.Unlock()
	if meter := m.speeds[jobID]; meter != nil {
		return meter
	}
	if len(m.speeds) >= maxSpeedMeters && len(m.speedOrder) > 0 {
		oldest := m.speedOrder[0]
		m.speedOrder = m.speedOrder[1:]
		delete(m.speeds, oldest)
	}
	meter := NewSpeedMeter(defaultSpeedWindow)
	m.speeds[jobID] = meter
	m.speedOrder = append(m.speedOrder, jobID)
	return meter
}

func acquireSlot(ctx context.Context, slots chan struct{}) error {
	if slots == nil {
		return nil
	}
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSlot(slots chan struct{}) {
	if slots != nil {
		<-slots
	}
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
	_ = m.db.AddDownloadEvent(ctx, job.ID, "", "state", "download started", m.now())
	m.publishJobSnapshot(job.ID)

	pendingCount := 0
	for index := range job.Files {
		if FileState(job.Files[index].State) == FileCompleted {
			continue
		}
		if FileState(job.Files[index].State) == FileMoving {
			recovered, recoverErr := m.recoverMovedFile(ctx, &job, &job.Files[index])
			if recoverErr != nil {
				return m.failJob(ctx, &job, &job.Files[index], recoverErr)
			}
			if recovered {
				continue
			}
		}
		if err := TransitionFileState(FileState(job.Files[index].State), FileDownloading); err != nil {
			return m.failJob(ctx, &job, &job.Files[index], err)
		}
		if err := m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{
			FileID: job.Files[index].ID, State: string(FileDownloading), UpdatedAt: m.now(),
		}); err != nil {
			return err
		}
		job.Files[index].State = string(FileDownloading)
		pendingCount++
	}

	if ctx.Err() != nil {
		m.pauseJob(context.Background(), job)
		return ctx.Err()
	}

	meter := m.getSpeedMeter(job.ID)
	var jobLimiter *BandwidthLimiter
	if m.perJobLimit > 0 {
		jobLimiter = NewBandwidthLimiter(m.perJobLimit)
	}
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	workerCount := m.maxActiveFiles
	if workerCount > pendingCount {
		workerCount = pendingCount
	}
	if workerCount < 1 && pendingCount > 0 {
		workerCount = 1
	}

	var next atomic.Int64
	var workers sync.WaitGroup
	var errorMu sync.Mutex
	var firstErr error
	var failedFile store.DownloadFileRecord

	claim := func() (store.DownloadFileRecord, bool) {
		for {
			index := int(next.Add(1) - 1)
			if index >= len(job.Files) {
				return store.DownloadFileRecord{}, false
			}
			file := job.Files[index]
			if FileState(file.State) == FileCompleted {
				continue
			}
			return file, true
		}
	}

	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				file, ok := claim()
				if !ok || workCtx.Err() != nil {
					return
				}
				if err := acquireSlot(workCtx, m.fileSlots); err != nil {
					return
				}
				err := m.processFileWithTransfer(workCtx, &job, &file, jobLimiter, meter)
				releaseSlot(m.fileSlots)
				if err != nil {
					errorMu.Lock()
					if firstErr == nil {
						firstErr = err
						failedFile = file
					}
					errorMu.Unlock()
					stop()
					return
				}
			}
		}()
	}
	workers.Wait()

	errorMu.Lock()
	transferErr := firstErr
	failure := failedFile
	errorMu.Unlock()
	if transferErr != nil {
		if IsQuotaError(transferErr) {
			var quota *QuotaError
			if !errors.As(transferErr, &quota) {
				quota = &QuotaError{Cause: transferErr}
			}
			return m.enterWaitingQuota(&job, &failure, quota)
		}
		if ctx.Err() != nil || errors.Is(transferErr, context.Canceled) || errors.Is(transferErr, context.DeadlineExceeded) {
			m.pauseJob(context.Background(), job)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return transferErr
		}
		if errors.Is(transferErr, ErrFinalizationPending) {
			_ = m.db.SetDownloadJobState(context.Background(), job.ID, string(JobPausedRecovery), m.now())
			return transferErr
		}
		return m.failJob(context.Background(), &job, &failure, transferErr)
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
	m.publishJobSnapshot(job.ID)
	if err := TransitionJobState(JobState(job.State), JobCompleted); err != nil {
		return err
	}
	if err := m.db.SetDownloadJobState(ctx, job.ID, string(JobCompleted), m.now()); err != nil {
		return err
	}
	_ = m.db.AddDownloadEvent(ctx, job.ID, "", "completed", "download completed", m.now())
	m.publishJobSnapshot(job.ID)
	return nil
}

func (m *Manager) processFile(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord) error {
	return m.processFileWithTransfer(ctx, job, file, nil, nil)
}

func (m *Manager) processFileWithTransfer(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord, limiter *BandwidthLimiter, meter *SpeedMeter) error {
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
	defer incompleteRoot.Close()
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

	bytesSinceCheckpoint := int64(0)
	dirty := false
	checkpoint := func() error {
		if err := m.checkpoint.Commit(ctx, Checkpoint{
			File:            partial,
			FileID:          file.ID,
			CompletedBitmap: bitmap.Clone(),
			BytesCommitted:  committed,
			State:           FileDownloading,
			UpdatedAt:       m.now(),
		}); err != nil {
			return err
		}
		dirty = false
		bytesSinceCheckpoint = 0
		return nil
	}

	pendingSegments := planner.Count - bitmap.Count()
	workerCount := m.workersPerFile
	if pendingSegments < int64(workerCount) {
		workerCount = int(pendingSegments)
	}
	if workerCount < 1 && pendingSegments > 0 {
		workerCount = 1
	}
	transferCtx, stop := context.WithCancel(ctx)
	defer stop()
	results := make(chan segmentResult, workerCount*2+1)
	var workers sync.WaitGroup
	var next int64
	claimBitmap := bitmap.Clone()
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				segmentIndex := atomic.AddInt64(&next, 1) - 1
				for segmentIndex < planner.Count && claimBitmap.IsSet(segmentIndex) {
					segmentIndex = atomic.AddInt64(&next, 1) - 1
				}
				if segmentIndex >= planner.Count || transferCtx.Err() != nil {
					return
				}
				segment, segmentErr := planner.Segment(segmentIndex)
				if segmentErr != nil {
					results <- segmentResult{err: segmentErr}
					stop()
					return
				}
				if acquireErr := acquireSlot(transferCtx, m.workerSlots); acquireErr != nil {
					results <- segmentResult{err: acquireErr}
					return
				}
				written, downloadErr := m.downloadSegmentWithRetry(transferCtx, job, *file, partial, key, payloadURL, segment, limiter, meter)
				releaseSlot(m.workerSlots)
				results <- segmentResult{segment: segment, written: written, err: downloadErr}
				if downloadErr != nil {
					stop()
					return
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	ticker := time.NewTicker(m.checkpointInterval)
	defer ticker.Stop()
	var transferErr error
	resultsOpen := true
	for resultsOpen {
		select {
		case result, ok := <-results:
			if !ok {
				resultsOpen = false
				continue
			}
			if result.err != nil {
				if transferErr == nil {
					transferErr = result.err
					stop()
				}
				continue
			}
			if transferErr != nil {
				continue
			}
			if result.written != result.segment.Size() {
				transferErr = fmt.Errorf("segment %d wrote %d bytes, want %d", result.segment.Index, result.written, result.segment.Size())
				stop()
				continue
			}
			if err := bitmap.Set(result.segment.Index); err != nil {
				transferErr = err
				stop()
				continue
			}
			committed += result.segment.Size()
			bytesSinceCheckpoint += result.segment.Size()
			dirty = true
			m.publishProgress(job.ID, file.ID, string(FileDownloading), committed, file.SizeBytes)
			if bytesSinceCheckpoint >= m.checkpointBytes {
				if checkpointErr := checkpoint(); checkpointErr != nil {
					transferErr = checkpointErr
					stop()
				}
			}
		case <-ticker.C:
			if dirty && transferErr == nil {
				if checkpointErr := checkpoint(); checkpointErr != nil {
					transferErr = checkpointErr
					stop()
				}
			}
		}
	}
	if transferErr != nil {
		if dirty {
			if checkpointErr := checkpoint(); checkpointErr != nil && ctx.Err() == nil {
				return checkpointErr
			}
		}
		return transferErr
	}
	if dirty || planner.Count == 0 {
		if err := checkpoint(); err != nil {
			return err
		}
	}
	if err := m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileVerifying), UpdatedAt: m.now()}); err != nil {
		return err
	}
	file.State = string(FileVerifying)
	m.publishJobSnapshot(job.ID)
	if err := VerifyPartialFileContext(ctx, partial.File(), file.SizeBytes, key); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileFailed), LastErrorCode: "integrity_mismatch", LastErrorMessage: err.Error(), UpdatedAt: m.now()})
		_ = m.db.AddDownloadEvent(context.Background(), job.ID, file.ID, "error", "file integrity verification failed", m.now())
		m.publishJobSnapshot(job.ID)
		return err
	}
	partialRelativePath := partial.RelativePath()
	if err := partial.Close(); err != nil {
		return fmt.Errorf("close partial file before finalization: %w", err)
	}
	closed = true
	finalRelativePath, err := m.finalize(ctx, job, file, partialRelativePath)
	if err != nil {
		if errors.Is(err, ErrFinalizationPending) {
			return err
		}
		code := "finalize_failed"
		if errors.Is(err, ErrCrossDevice) {
			code = "cross_device_finalize"
		}
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileFailed), LastErrorCode: code, LastErrorMessage: err.Error(), UpdatedAt: m.now()})
		m.publishJobSnapshot(job.ID)
		return err
	}
	persistCtx := ctx
	cancelPersist := func() {}
	if ctx.Err() != nil {
		persistCtx, cancelPersist = context.WithTimeout(context.Background(), 10*time.Second)
	}
	defer cancelPersist()
	if err := m.db.CompleteDownloadFile(persistCtx, file.ID, finalRelativePath, m.now()); err != nil {
		return fmt.Errorf("%w: persist completed file: %v", ErrFinalizationPending, err)
	}
	file.State = string(FileCompleted)
	file.FinalRelativePath = finalRelativePath
	file.BytesCommitted = file.SizeBytes
	m.publishJobSnapshot(job.ID)
	return nil
}

func (m *Manager) downloadSegmentWithRetry(ctx context.Context, job *store.DownloadJobRecord, file store.DownloadFileRecord, writer interface {
	WriteAt([]byte, int64) (int, error)
}, key mega.FileKey, payloadURL string, segment Segment, limiter *BandwidthLimiter, meter *SpeedMeter) (int64, error) {
	worker, client, err := m.workerForJob(ctx, job)
	if err != nil {
		return 0, err
	}
	refreshes := 0
	for retry := 0; ; retry++ {
		written, err := worker.DownloadRangeWithOptions(ctx, writer, key, payloadURL, segment, file.SizeBytes, TransferOptions{Limiter: limiter, GlobalLimiter: m.globalLimiter, Meter: meter, ReadIdleTimeout: m.readIdleTimeout})
		if err == nil {
			return written, nil
		}
		class := ClassifyRetry(err)
		if class == RetryQuota {
			var status *HTTPStatusError
			retryAfter := time.Duration(0)
			if errors.As(err, &status) {
				retryAfter, _ = ParseRetryAfter(status.RetryAfter, m.now())
			}
			return written, &QuotaError{Cause: err, RetryAfter: retryAfter}
		}
		if class == RetryRefreshURL && refreshes < 1 {
			refreshes++
			newURL, refreshErr := m.refreshPayloadURL(ctx, job, &file, client)
			if refreshErr != nil && errors.Is(refreshErr, mega.ErrSessionRejected) && job.AccountID != "" {
				// A payload URL can expire at the same time as the account
				// session. Rebuild the bounded account client once; the lifecycle
				// performs at most one stored-session validation/password fallback.
				m.InvalidateAccountSession(job.AccountID)
				var authErr error
				worker, client, authErr = m.workerForJob(ctx, job)
				if authErr == nil {
					newURL, refreshErr = m.refreshPayloadURL(ctx, job, &file, client)
				} else {
					refreshErr = authErr
				}
			}
			if refreshErr == nil {
				payloadURL = newURL
				continue
			}
			err = fmt.Errorf("refresh MEGA payload URL: %w", refreshErr)
			class = ClassifyRetry(err)
		}
		if (class != RetryTransport && class != RetryRateLimit && class != RetryServer) || retry >= m.normalRetryLimit {
			return written, err
		}
		statusRetryAfter := ""
		var status *HTTPStatusError
		if errors.As(err, &status) {
			statusRetryAfter = status.RetryAfter
		}
		delay := ExponentialBackoff(retry+1, statusRetryAfter, m.now(), nil)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return written, ctx.Err()
		}
	}
}

func (m *Manager) workerForJob(ctx context.Context, job *store.DownloadJobRecord) (*RangeWorker, *mega.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	accountKey := ""
	if job.AccountID != "" {
		accountKey = job.AccountID + "\x00" + job.ProxyID
		m.accountMu.Lock()
		cached := m.accountClients[accountKey]
		m.accountMu.Unlock()
		if cached != nil {
			return NewRangeWorker(cached), cached, nil
		}
	}
	client := m.mega
	// Select the immutable transport before account authentication so all
	// prelogin, login, session validation, and payload commands use the same
	// configured route.
	if job.ProxyID != "" && m.transportPool == nil {
		return nil, nil, fmt.Errorf("proxy transport pool is unavailable")
	}
	if job.ProxyID != "" {
		profile, err := m.db.GetProxyProfile(ctx, job.ProxyID)
		if err != nil {
			return nil, nil, fmt.Errorf("load proxy profile: %w", err)
		}
		if !profile.Enabled {
			return nil, nil, fmt.Errorf("proxy profile is disabled")
		}
		passwordCipher, err := m.db.ProxySecret(ctx, job.ProxyID)
		if err != nil {
			return nil, nil, err
		}
		password := ""
		if len(passwordCipher) > 0 {
			plain, decryptErr := m.secrets.Decrypt(passwordCipher)
			if decryptErr != nil {
				return nil, nil, fmt.Errorf("proxy credentials are unavailable")
			}
			password = string(plain)
			clear(plain)
		}
		httpClient, err := m.transportPool.Client(network.ProxyProfile{ID: profile.ID, Name: profile.Name, Type: network.ProxyType(profile.Type), Host: profile.Host, Port: profile.Port, Username: profile.Username, Password: password, Timeout: time.Duration(profile.TimeoutSeconds) * time.Second, Enabled: profile.Enabled})
		password = ""
		if err != nil {
			return nil, nil, err
		}
		client = client.WithHTTPClient(httpClient)
	}
	if job.AccountID != "" {
		m.accountMu.Lock()
		if cached := m.accountClients[accountKey]; cached != nil {
			client = cached
			m.accountMu.Unlock()
		} else {
			var err error
			client, err = mega.NewAccountLifecycle(client, m.db, m.secrets).ClientFor(ctx, job.AccountID)
			if err == nil {
				if m.accountClients == nil {
					m.accountClients = make(map[string]*mega.Client)
				}
				if len(m.accountClients) >= maxCachedAccountClients {
					for cachedKey := range m.accountClients {
						delete(m.accountClients, cachedKey)
						break
					}
				}
				m.accountClients[accountKey] = client
			}
			m.accountMu.Unlock()
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if job.ProxyID == "" && job.AccountID == "" {
		return m.worker, client, nil
	}
	return NewRangeWorker(client), client, nil
}

func (m *Manager) refreshPayloadURL(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord, client *mega.Client) (string, error) {
	raw, err := m.secrets.Decrypt(job.SourceKeyCiphertext)
	if err != nil {
		return "", err
	}
	link := mega.PublicLink{Kind: mega.LinkKind(job.SourceKind), Handle: job.SourceHandle, Key: string(raw), SelectedPath: job.SourceSelectedPath, SelectedNode: job.SourceSelectedNode}
	url, err := client.RefreshPayloadURL(ctx, link, file.RemoteNodeID)
	if err != nil {
		return "", err
	}
	ciphertext, err := m.secrets.Encrypt([]byte(url))
	if err != nil {
		return "", err
	}
	if err := m.db.UpdateDownloadPayloadURL(context.Background(), file.ID, ciphertext, m.now()); err != nil {
		return "", err
	}
	file.PayloadURLCiphertext = ciphertext
	return url, nil
}

type segmentResult struct {
	segment Segment
	written int64
	err     error
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
	if file != nil {
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: file.ID, State: string(FileFailed), LastErrorCode: "download_failed", LastErrorMessage: cause.Error(), UpdatedAt: m.now()})
	}
	_ = m.db.AddDownloadEvent(context.Background(), job.ID, fileID(file), "error", cause.Error(), m.now())
	current := *job
	if persisted, err := m.db.GetDownloadJob(context.Background(), job.ID); err == nil {
		current = persisted
	}
	for _, candidate := range current.Files {
		if file != nil && candidate.ID == file.ID || FileState(candidate.State) == FileCompleted || FileState(candidate.State) == FileFailed {
			continue
		}
		_ = m.db.UpdateDownloadFileState(context.Background(), store.DownloadFileStateUpdate{FileID: candidate.ID, State: string(FileFailed), LastErrorCode: "download_aborted", LastErrorMessage: cause.Error(), UpdatedAt: m.now()})
	}
	if TransitionJobState(JobState(current.State), JobFailed) == nil {
		_ = m.db.SetDownloadJobState(context.Background(), job.ID, string(JobFailed), m.now())
	}
	m.publishJobSnapshot(job.ID)
	return cause
}

func fileID(file *store.DownloadFileRecord) string {
	if file == nil {
		return ""
	}
	return file.ID
}

func (m *Manager) pauseJob(ctx context.Context, job store.DownloadJobRecord) {
	current := job
	if persisted, err := m.db.GetDownloadJob(ctx, job.ID); err == nil {
		current = persisted
	}
	for _, file := range current.Files {
		state := FileState(file.State)
		if state == FileDownloading || state == FileVerifying || state == FileMoving {
			_ = m.db.UpdateDownloadFileState(ctx, store.DownloadFileStateUpdate{FileID: file.ID, State: string(FilePaused), UpdatedAt: m.now()})
		}
	}
	if TransitionJobState(JobState(current.State), JobPaused) == nil {
		_ = m.db.SetDownloadJobState(ctx, job.ID, string(JobPaused), m.now())
	}
	_ = m.db.AddDownloadEvent(ctx, job.ID, "", "state", "download paused", m.now())
	m.publishJobSnapshot(job.ID)
}

func (m *Manager) recoverMovedFile(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord) (bool, error) {
	incompleteRoot, err := fsRoot(job.IncompleteRoot)
	if err != nil {
		return false, err
	}
	defer incompleteRoot.Close()
	partialRelative, err := PartialRelativePath(job.ID, file.RemotePath)
	if err != nil {
		return false, err
	}
	partialExists := false
	if _, statErr := incompleteRoot.Lstat(partialRelative); statErr == nil {
		partialExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("inspect partial file during move recovery: %w", statErr)
	}

	completeRoot, err := fsRoot(job.CompleteRoot)
	if err != nil {
		return false, err
	}
	defer completeRoot.Close()
	targetInfo, statErr := completeRoot.Lstat(file.FinalRelativePath)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			if partialExists {
				return false, nil
			}
			return false, fmt.Errorf("move recovery found neither partial file nor final target %q", file.FinalRelativePath)
		}
		return false, fmt.Errorf("inspect final target during move recovery: %w", statErr)
	}
	if targetInfo.IsSymlink() {
		return false, fmt.Errorf("%w: recovered final target is a symlink", fsroot.ErrSymlink)
	}
	rawKey, err := m.secrets.Decrypt(file.FileKeyCiphertext)
	if err != nil {
		return false, fmt.Errorf("decrypt file key during move recovery: %w", err)
	}
	key, err := mega.DecodeFileKeyBytes(rawKey)
	if err != nil {
		return false, err
	}
	targetFile, err := completeRoot.OpenFile(file.FinalRelativePath, os.O_RDONLY, 0)
	if err != nil {
		return false, fmt.Errorf("open final target during move recovery: %w", err)
	}
	verifyErr := VerifyPartialFileContext(ctx, targetFile, file.SizeBytes, key)
	closeErr := targetFile.Close()
	if verifyErr != nil {
		if partialExists {
			return false, nil
		}
		return false, fmt.Errorf("verify final target during move recovery: %w", verifyErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close final target during move recovery: %w", closeErr)
	}
	if partialExists {
		if err := incompleteRoot.Remove(partialRelative); err != nil {
			return false, fmt.Errorf("remove recovered partial link: %w", err)
		}
		if err := incompleteRoot.SyncDir(relativeParent(partialRelative)); err != nil {
			return false, fmt.Errorf("sync recovered incomplete directory: %w", err)
		}
	}
	if err := m.db.CompleteDownloadFile(ctx, file.ID, file.FinalRelativePath, m.now()); err != nil {
		return false, err
	}
	file.State = string(FileCompleted)
	return true, nil
}

var (
	ErrTransferRootsNotConfigured = errors.New("transfer roots are not configured")
	ErrCrossDevice                = errors.New("incomplete and complete roots must share a filesystem")
	ErrFinalizationPending        = errors.New("atomic rename succeeded but completion persistence is pending")
)

func validateTransferRoots(job store.DownloadJobRecord) error {
	if job.CompleteRoot == "" || job.IncompleteRoot == "" {
		return fmt.Errorf("%w for download %q", ErrTransferRootsNotConfigured, job.ID)
	}
	completeRoot, err := fsroot.New(job.CompleteRoot)
	if err != nil {
		return fmt.Errorf("invalid complete root for download %q: %w", job.ID, err)
	}
	if err := completeRoot.Close(); err != nil {
		return fmt.Errorf("close complete root for download %q: %w", job.ID, err)
	}
	incompleteRoot, err := fsroot.New(job.IncompleteRoot)
	if err != nil {
		return fmt.Errorf("invalid incomplete root for download %q: %w", job.ID, err)
	}
	if err := incompleteRoot.Close(); err != nil {
		return fmt.Errorf("close incomplete root for download %q: %w", job.ID, err)
	}
	return nil
}

func (m *Manager) finalize(ctx context.Context, job *store.DownloadJobRecord, file *store.DownloadFileRecord, partialRelative string) (string, error) {
	incompleteRoot, err := fsRoot(job.IncompleteRoot)
	if err != nil {
		return "", err
	}
	defer incompleteRoot.Close()
	completeRoot, err := fsRoot(job.CompleteRoot)
	if err != nil {
		return "", err
	}
	defer completeRoot.Close()
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
	for attempt := 0; attempt <= 100_000; attempt++ {
		target, planErr := completeRoot.PlanConflict(file.FinalRelativePath, policy)
		if planErr != nil {
			return "", planErr
		}
		// Persist the exact candidate before attempting the atomic move. If
		// the no-replace rename loses a concurrent conflict race, the next
		// iteration selects and persists a new exact candidate.
		if err := m.db.PrepareDownloadFileMove(ctx, file.ID, target, m.now()); err != nil {
			return "", err
		}
		overwrite := policy == fsroot.ConflictOverwrite
		renameErr := completeRoot.RenameFrom(incompleteRoot, partialRelative, target, overwrite)
		if errors.Is(renameErr, fsroot.ErrConflict) && policy == fsroot.ConflictRename {
			continue
		}
		if renameErr != nil {
			if errors.Is(renameErr, syscall.EXDEV) {
				return "", fmt.Errorf("%w: cannot atomically rename %q into complete root", ErrCrossDevice, file.FinalRelativePath)
			}
			return "", renameErr
		}
		if err := completeRoot.SyncDir(relativeParent(target)); err != nil {
			return "", fmt.Errorf("%w: sync complete directory: %v", ErrFinalizationPending, err)
		}
		if err := incompleteRoot.SyncDir(relativeParent(partialRelative)); err != nil {
			return "", fmt.Errorf("%w: sync incomplete directory: %v", ErrFinalizationPending, err)
		}
		return target, nil
	}
	return "", fmt.Errorf("%w: exhausted atomic conflict candidates", fsroot.ErrConflict)
}

func relativeParent(relative string) string {
	parent := filepath.Dir(relative)
	if parent == "." {
		return ""
	}
	return parent
}

func fsRoot(path string) (*fsroot.Root, error) {
	return fsroot.New(path)
}
