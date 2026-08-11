package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

func TestPhaseEOneAndFourWorkersProduceIdenticalHashes(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 8 << 20, Delay: 2 * time.Millisecond})
	defer fixture.Close()

	oneManager, oneDB, oneSecrets, oneRoots := newPhaseEManager(t, fixture, 1, 1, 1, 0)
	oneID := "phase-e-one"
	insertPhaseDJobWithSegment(t, oneDB, oneSecrets, fixture, oneRoots, oneID, fixture.FileLink(), 64<<10)
	if err := oneManager.RunJob(context.Background(), oneID); err != nil {
		t.Fatal(err)
	}
	oneOutput, err := os.ReadFile(filepath.Join(oneRoots.complete, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}

	fourManager, fourDB, fourSecrets, fourRoots := newPhaseEManager(t, fixture, 4, 1, 4, 0)
	fourID := "phase-e-four"
	insertPhaseDJobWithSegment(t, fourDB, fourSecrets, fixture, fourRoots, fourID, fixture.FileLink(), 64<<10)
	if err := fourManager.RunJob(context.Background(), fourID); err != nil {
		t.Fatal(err)
	}
	fourOutput, err := os.ReadFile(filepath.Join(fourRoots.complete, "fixture.txt"))
	if err != nil {
		t.Fatal(err)
	}
	oneHash := sha256.Sum256(oneOutput)
	fourHash := sha256.Sum256(fourOutput)
	if oneHash != fourHash || !bytes.Equal(oneOutput, fourOutput) {
		t.Fatalf("one-worker and four-worker output hashes differ: %x != %x", oneHash, fourHash)
	}
	if snapshot := fourManager.Speed(fourID); snapshot.TotalBytes != int64(len(fixture.Plaintext())) {
		t.Fatalf("four-worker speed meter total = %d, want %d", snapshot.TotalBytes, len(fixture.Plaintext()))
	}
}

func TestPhaseEGlobalWorkerSemaphoreBoundsPayloadConcurrency(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 4 << 20, Delay: 20 * time.Millisecond})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 8, 1, 2, 0)
	jobID := "phase-e-global-semaphore"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 64<<10)
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	if got := fixture.MaxConcurrentPayloadRequests(); got > 2 {
		t.Fatalf("max payload concurrency = %d, exceeded global worker limit", got)
	}
	if got := fixture.MaxConcurrentPayloadRequests(); got < 2 {
		t.Fatalf("max payload concurrency = %d, expected parallel workers", got)
	}
}

func TestPhaseEManagerRejectsConcurrentExecutionOfSameJob(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 2 << 20, Delay: 20 * time.Millisecond})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 2, 2, 4, 0)
	jobID := "phase-e-single-owner"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 64<<10)
	done := make(chan error, 1)
	go func() { done <- manager.RunJob(context.Background(), jobID) }()
	waitForJobState(t, db, jobID, download.JobDownloading)
	if err := manager.RunJob(context.Background(), jobID); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second execution error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPhaseEBandwidthLimiterCapsNetworkReads(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 4 << 20})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 1, 1, 1, 4<<20)
	jobID := "phase-e-bandwidth"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 1<<20)
	started := time.Now()
	if err := manager.RunJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 400*time.Millisecond {
		t.Fatalf("limited transfer completed in %s; limiter was not applied", elapsed)
	}
}

func TestPhaseENoGoroutineOrFileDescriptorGrowth(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 1 << 20})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 4, 2, 8, 0)
	warmupID := "phase-e-resource-warmup"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, warmupID, fixture.FileLink(), 64<<10)
	if err := manager.RunJob(context.Background(), warmupID); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	baselineGoroutines := runtime.NumGoroutine()
	baselineFDs := descriptorCount(t)
	for index := 0; index < 7; index++ {
		jobID := "phase-e-resource-" + string(rune('a'+index))
		insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 64<<10)
		if err := manager.RunJob(context.Background(), jobID); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	if got := runtime.NumGoroutine(); got > baselineGoroutines+4 {
		t.Fatalf("goroutines grew from %d to %d", baselineGoroutines, got)
	}
	if got := descriptorCount(t); got > baselineFDs+4 {
		t.Fatalf("file descriptors grew from %d to %d", baselineFDs, got)
	}
}

func TestPhaseEQueuePriorityAndFIFO(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 2 << 20, Delay: 4 * time.Millisecond})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 1, 1, 1, 0)
	for _, jobID := range []string{"phase-e-fifo-a", "phase-e-priority", "phase-e-fifo-c"} {
		insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 64<<10)
	}
	if err := manager.StartJobWithPriority("phase-e-fifo-a", 0); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJobWithPriority("phase-e-priority", 10); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJobWithPriority("phase-e-fifo-c", 0); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, jobID := range []string{"phase-e-fifo-a", "phase-e-priority", "phase-e-fifo-c"} {
		waitForJobState(t, db, jobID, download.JobCompleted)
	}
	rows, err := db.Query(`SELECT job_id FROM download_events WHERE kind = 'completed' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var order []string
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			t.Fatal(err)
		}
		order = append(order, jobID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"phase-e-priority", "phase-e-fifo-a", "phase-e-fifo-c"}
	if len(order) != len(want) {
		t.Fatalf("completion order = %v, want %v", order, want)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("completion order = %v, want %v", order, want)
		}
	}
}

func TestPhaseEPauseResumeAndCancelPreserveOrDeletePartialData(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 8 << 20, Delay: 25 * time.Millisecond})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 4, 1, 4, 0)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	pauseID := "phase-e-pause"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, pauseID, fixture.FileLink(), 64<<10)
	if err := db.SetDownloadJobState(context.Background(), pauseID, string(download.JobQueued), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJob(pauseID); err != nil {
		t.Fatal(err)
	}
	waitForAnyJobState(t, db, pauseID, download.JobDownloading)
	if err := manager.PauseJob(context.Background(), pauseID); err != nil {
		t.Fatal(err)
	}
	waitForJobState(t, db, pauseID, download.JobPaused)
	if err := manager.ResumeJob(context.Background(), pauseID); err != nil {
		t.Fatal(err)
	}
	waitForJobState(t, db, pauseID, download.JobCompleted)

	keepID := "phase-e-cancel-keep"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, keepID, fixture.FileLink(), 64<<10)
	if err := db.SetDownloadJobState(context.Background(), keepID, string(download.JobQueued), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJob(keepID); err != nil {
		t.Fatal(err)
	}
	waitForAnyJobState(t, db, keepID, download.JobDownloading)
	if err := manager.CancelJob(context.Background(), keepID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(roots.incomplete, keepID)); err != nil {
		t.Fatalf("cancelled partial directory was removed: %v", err)
	}

	deleteID := "phase-e-cancel-delete"
	insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, deleteID, fixture.FileLink(), 64<<10)
	if err := db.SetDownloadJobState(context.Background(), deleteID, string(download.JobQueued), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJob(deleteID); err != nil {
		t.Fatal(err)
	}
	waitForAnyJobState(t, db, deleteID, download.JobDownloading)
	if err := manager.CancelJob(context.Background(), deleteID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(roots.incomplete, deleteID)); !os.IsNotExist(err) {
		t.Fatalf("cancelled partial directory still exists, stat error = %v", err)
	}
}

func TestPhaseEPausePreservesFileThatCompletedWhileSiblingWasActive(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 1, 2, 2, 0)
	jobID := "phase-e-pause-completed-sibling"
	record := insertPhaseEJobWithFiles(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 64<<10, 2)
	started := make(chan struct{})
	var once sync.Once
	blocked := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		once.Do(func() { close(started) })
		<-request.Context().Done()
	}))
	defer blocked.Close()
	blockedURL, err := secrets.Encrypt([]byte(blocked.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE download_files SET payload_url_ciphertext = ? WHERE id = ?`, blockedURL, record.Files[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDownloadJobState(context.Background(), jobID, string(download.JobQueued), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.StartJob(jobID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked sibling did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, readErr := db.GetDownloadJob(context.Background(), jobID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if current.Files[0].State == string(download.FileCompleted) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := manager.PauseJob(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	current, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Files[0].State != string(download.FileCompleted) {
		t.Fatalf("completed sibling regressed to %q during pause", current.Files[0].State)
	}
	if current.Files[1].State != string(download.FilePaused) {
		t.Fatalf("active sibling state = %q, want paused", current.Files[1].State)
	}
}

func TestPhaseEFailurePreservesFileCompletedByParallelSibling(t *testing.T) {
	fixture := NewFakeMegaServer()
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManager(t, fixture, 1, 2, 2, 0)
	jobID := "phase-e-failure-completed-sibling"
	record := insertPhaseEJobWithFiles(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 64<<10, 2)
	failing := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(250 * time.Millisecond)
		writer.Header().Set("Retry-After", "0")
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	failingURL, err := secrets.Encrypt([]byte(failing.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE download_files SET payload_url_ciphertext = ? WHERE id = ?`, failingURL, record.Files[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.RunJob(context.Background(), jobID); err == nil {
		t.Fatal("parallel job unexpectedly completed")
	}
	current, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Files[0].State != string(download.FileCompleted) {
		t.Fatalf("completed sibling regressed to %q after peer failure", current.Files[0].State)
	}
	if current.Files[1].State != string(download.FileFailed) {
		t.Fatalf("failing sibling state = %q", current.Files[1].State)
	}
}

func TestPhaseECheckpointIntervalPersistsWhileAnotherSegmentIsBlocked(t *testing.T) {
	fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 2 << 20})
	defer fixture.Close()
	manager, db, secrets, roots := newPhaseEManagerWithCheckpoint(t, fixture, 2, 1, 2, 100*time.Millisecond)
	jobID := "phase-e-periodic-checkpoint"
	file := insertPhaseDJobWithSegment(t, db, secrets, fixture, roots, jobID, fixture.FileLink(), 1<<20)
	payload := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start, end, _, err := requestedRange(request.Header.Get("Range"), int64(len(fixture.Plaintext())))
		if err != nil {
			writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if start > 0 {
			<-request.Context().Done()
			return
		}
		ciphertext, err := downloadFixtureCiphertext(fixture, start, end)
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Range", contentRange(start, end, int64(len(fixture.Plaintext())), false))
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(ciphertext)
	}))
	defer payload.Close()
	payloadURL, err := secrets.Encrypt([]byte(payload.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE download_files SET payload_url_ciphertext = ? WHERE id = ?`, payloadURL, file.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.RunJob(ctx, jobID) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, readErr := db.GetDownloadJob(context.Background(), jobID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if current.Files[0].BytesCommitted == 1<<20 {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("completed segment was not persisted at the checkpoint interval")
}

func downloadFixtureCiphertext(fixture *FakeMegaServer, start, end int64) ([]byte, error) {
	return mega.CryptAt(fixture.Plaintext()[start:end+1], fixture.FileKey(), start)
}

func BenchmarkPhaseEDefaultWorkers(b *testing.B) {
	for iteration := 0; iteration < b.N; iteration++ {
		fixture := NewFakeMegaServerWithOptions(FakeMegaServerOptions{PayloadSize: 64 << 20, Delay: 5 * time.Millisecond})
		manager, db, secrets, roots := newPhaseEManager(b, fixture, 4, 2, 8, 0)
		jobID := "phase-e-benchmark"
		record := insertPhaseEJobWithFiles(b, db, secrets, fixture, roots, jobID, fixture.FileLink(), 1<<20, 2)
		var monitor sync.WaitGroup
		stopMonitor := make(chan struct{})
		var peakRSS, peakGoroutines, peakFDs int64
		monitor.Add(1)
		go func() {
			defer monitor.Done()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopMonitor:
					return
				case <-ticker.C:
					if rss := processRSSBytes(); rss > peakRSS {
						peakRSS = rss
					}
					if goroutines := int64(runtime.NumGoroutine()); goroutines > peakGoroutines {
						peakGoroutines = goroutines
					}
					if fds := int64(processFDCount()); fds > peakFDs {
						peakFDs = fds
					}
				}
			}
		}()
		start := time.Now()
		runErr := manager.RunJob(context.Background(), jobID)
		close(stopMonitor)
		monitor.Wait()
		if runErr != nil {
			b.Fatal(runErr)
		}
		b.ReportMetric(float64(time.Since(start).Microseconds()), "download_us")
		b.ReportMetric(float64(fixture.MaxConcurrentPayloadRequests()), "max_payload_workers")
		b.ReportMetric(float64(peakRSS), "max_rss_bytes")
		b.ReportMetric(float64(peakGoroutines), "max_goroutines")
		b.ReportMetric(float64(peakFDs), "max_fds")
		allocated := allocatedDiskBytes(filepath.Dir(roots.incomplete))
		payloadBytes := record.TotalBytes
		overhead := allocated - payloadBytes
		if overhead < 0 {
			overhead = 0
		}
		b.ReportMetric(float64(overhead), "disk_overhead_bytes")
		fixture.Close()
	}
}

func insertPhaseEJobWithFiles(tb testing.TB, db *store.DB, secrets *store.SecretStore, fixture *FakeMegaServer, roots phaseDRoots, jobID, linkURL string, segmentSize int64, fileCount int) store.DownloadJobRecord {
	tb.Helper()
	job, err := fixture.Client().ResolveLink(context.Background(), linkURL, "")
	if err != nil {
		tb.Fatal(err)
	}
	if len(job.Files) != 1 {
		tb.Fatalf("fixture files = %d", len(job.Files))
	}
	file := job.Files[0]
	planner, err := download.NewSegmentPlanner(file.Size, segmentSize)
	if err != nil {
		tb.Fatal(err)
	}
	sourceKey, err := secrets.Encrypt([]byte(mustParsedLinkKey(tb, linkURL)))
	if err != nil {
		tb.Fatal(err)
	}
	files := make([]store.DownloadFileInput, 0, fileCount)
	for index := 0; index < fileCount; index++ {
		fileKey, encryptErr := secrets.Encrypt(file.FileKey)
		if encryptErr != nil {
			tb.Fatal(encryptErr)
		}
		payloadURL, encryptErr := secrets.Encrypt([]byte(file.PayloadURL))
		if encryptErr != nil {
			tb.Fatal(encryptErr)
		}
		name := fmt.Sprintf("fixture-%d.txt", index)
		files = append(files, store.DownloadFileInput{
			ID:                   fmt.Sprintf("%s-file-%d", jobID, index),
			RemoteNodeID:         fmt.Sprintf("%s-node-%d", file.NodeID, index),
			RemotePath:           name,
			FinalRelativePath:    name,
			SizeBytes:            file.Size,
			SegmentSizeBytes:     segmentSize,
			SegmentCount:         planner.Count,
			FileKeyCiphertext:    fileKey,
			PayloadURLCiphertext: payloadURL,
			State:                string(download.FilePending),
		})
	}
	record, err := db.InsertDownloadJob(context.Background(), store.DownloadJobInput{
		ID:                  jobID,
		SourceKind:          string(job.Kind),
		SourceHandle:        "fixture",
		SourceKeyCiphertext: sourceKey,
		DisplayName:         job.DisplayName,
		TotalBytes:          job.TotalBytes * int64(fileCount),
		CompleteRoot:        roots.complete,
		IncompleteRoot:      roots.incomplete,
		State:               string(download.JobReady),
		Files:               files,
	})
	if err != nil {
		tb.Fatal(err)
	}
	return record
}

func newPhaseEManager(tb testing.TB, fixture *FakeMegaServer, workersPerFile, maxActiveFiles, maxGlobalWorkers int, globalLimit int64) (*download.Manager, *store.DB, *store.SecretStore, phaseDRoots) {
	return newPhaseEManagerWithLimits(tb, fixture, workersPerFile, maxActiveFiles, maxGlobalWorkers, globalLimit, time.Second)
}

func newPhaseEManagerWithCheckpoint(tb testing.TB, fixture *FakeMegaServer, workersPerFile, maxActiveFiles, maxGlobalWorkers int, checkpointInterval time.Duration) (*download.Manager, *store.DB, *store.SecretStore, phaseDRoots) {
	return newPhaseEManagerWithLimits(tb, fixture, workersPerFile, maxActiveFiles, maxGlobalWorkers, 0, checkpointInterval)
}

func newPhaseEManagerWithLimits(tb testing.TB, fixture *FakeMegaServer, workersPerFile, maxActiveFiles, maxGlobalWorkers int, globalLimit int64, checkpointInterval time.Duration) (*download.Manager, *store.DB, *store.SecretStore, phaseDRoots) {
	tb.Helper()
	root := tb.TempDir()
	roots := phaseDRoots{incomplete: filepath.Join(root, "incomplete"), complete: filepath.Join(root, "complete")}
	db, err := store.Open(context.Background(), filepath.Join(root, "megadw.sqlite3"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	secrets, err := store.OpenSecretStore(filepath.Join(root, "secret.key"))
	if err != nil {
		tb.Fatal(err)
	}
	service, err := settings.NewService(db)
	if err != nil {
		tb.Fatal(err)
	}
	manager, err := download.NewManager(download.Config{
		DB:                             db,
		Secrets:                        secrets,
		Mega:                           fixture.Client(),
		Settings:                       service,
		CheckpointInterval:             checkpointInterval,
		CheckpointBytes:                1 << 30,
		WorkersPerFile:                 workersPerFile,
		MaxActiveFiles:                 maxActiveFiles,
		MaxGlobalWorkers:               maxGlobalWorkers,
		GlobalSpeedLimitBytesPerSecond: globalLimit,
	})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = manager.Close() })
	return manager, db, secrets, roots
}

func waitForJobState(tb testing.TB, db *store.DB, jobID string, want download.JobState) {
	tb.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		job, err := db.GetDownloadJob(context.Background(), jobID)
		if err != nil {
			tb.Fatal(err)
		}
		if download.JobState(job.State) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, err := db.GetDownloadJob(context.Background(), jobID)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Fatalf("job %q state = %q, want %q", jobID, job.State, want)
}

func waitForAnyJobState(tb testing.TB, db *store.DB, jobID string, want download.JobState) {
	waitForJobState(tb, db, jobID, want)
}

func descriptorCount(tb testing.TB) int {
	tb.Helper()
	count, err := processFDCountWithError()
	if err != nil {
		tb.Fatal(err)
	}
	return count
}

func processFDCount() int {
	count, _ := processFDCountWithError()
	return count
}

func processFDCountWithError() (int, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func processRSSBytes() int64 {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kilobytes * 1024
	}
	return 0
}

func allocatedDiskBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			total += stat.Blocks * 512
		} else {
			total += info.Size()
		}
		return nil
	})
	return total
}
