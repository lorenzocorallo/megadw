// Command megad-benchmark runs the release resource matrix against the same
// resumable manager used by the application. The deterministic fixture is a
// separate process, so downloader RSS and CPU measurements do not include the
// fixture's plaintext or encryption buffers.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/internal/download"
	"github.com/lorenzocorallo/megadw/internal/mega"
	"github.com/lorenzocorallo/megadw/internal/settings"
	"github.com/lorenzocorallo/megadw/internal/store"
)

const (
	defaultFixtureSize  = 256 << 20
	defaultFixtureDelay = 150 * time.Millisecond
	segmentSize         = int64(8 << 20)
)

type profile struct {
	Name    string
	CPUs    int
	Workers []int
}

var profiles = map[string]profile{
	"small": {Name: "small", CPUs: 2, Workers: []int{1, 4, 8}},
	"large": {Name: "large", CPUs: 4, Workers: []int{8, 12}},
}

type measurement struct {
	Profile                   string  `json:"profile"`
	TargetCPUs                int     `json:"targetCPUs"`
	Workers                   int     `json:"workers"`
	PayloadBytes              int64   `json:"payloadBytes"`
	DurationSeconds           float64 `json:"durationSeconds"`
	ThroughputMiBPerSecond    float64 `json:"throughputMiBPerSecond"`
	RSSMiBMax                 float64 `json:"rssMiBMax"`
	CPUPercentOneCore         float64 `json:"cpuPercentOneCore"`
	CPUPercentOfTargetProfile float64 `json:"cpuPercentOfTargetProfile"`
	OpenFDsMax                int     `json:"openFDsMax"`
	GoroutinesMax             int     `json:"goroutinesMax"`
	SQLiteWriteTxPerSecond    float64 `json:"sqliteWriteTransactionsPerSecond"`
	SQLiteCheckpointPerSecond float64 `json:"sqliteCheckpointTransactionsPerSecond"`
	TempDiskOverheadMiBMax    float64 `json:"temporaryDiskOverheadMiBMax"`
	ConstraintNote            string  `json:"constraintNote"`
}

type fixtureInfo struct {
	baseURL string
	link    string
}

func main() {
	profileName := flag.String("profile", "all", "small, large, or all")
	size := flag.Int64("size", defaultFixtureSize, "deterministic fixture size in bytes")
	delay := flag.Duration("delay", defaultFixtureDelay, "fixture delay per ranged response")
	fixtureBinary := flag.String("fixture-binary", os.Getenv("MEGADW_FIXTURE_BINARY"), "fixture helper binary")
	flag.Parse()

	if *size < segmentSize {
		fatalf("fixture size must be at least %d bytes", segmentSize)
	}
	if *fixtureBinary == "" {
		*fixtureBinary = filepath.Join(filepath.Dir(os.Args[0]), "megad-bench-fixture")
	}
	selected, err := selectProfiles(*profileName)
	if err != nil {
		fatalf("select profile: %v", err)
	}

	for _, current := range selected {
		if current.CPUs > runtime.NumCPU() {
			fmt.Fprintf(os.Stderr, "warning: target profile %s requests %d CPUs but this host exposes %d; GOMAXPROCS is capped by the host\n", current.Name, current.CPUs, runtime.NumCPU())
		}
		runtime.GOMAXPROCS(current.CPUs)
		for _, workers := range current.Workers {
			result, err := runMeasurement(current, workers, *size, *delay, *fixtureBinary)
			if err != nil {
				fatalf("profile %s workers %d: %v", current.Name, workers, err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				fatalf("encode result: %v", err)
			}
			fmt.Println(string(encoded))
		}
	}
}

func selectProfiles(name string) ([]profile, error) {
	if name == "all" {
		return []profile{profiles["small"], profiles["large"]}, nil
	}
	current, ok := profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	return []profile{current}, nil
}

func runMeasurement(current profile, workers int, size int64, delay time.Duration, fixtureBinary string) (measurement, error) {
	fixture, stopFixture, err := startFixture(fixtureBinary, size, delay)
	if err != nil {
		return measurement{}, err
	}
	defer stopFixture()

	runRoot, err := os.MkdirTemp("", "megadw-resource-")
	if err != nil {
		return measurement{}, err
	}
	defer os.RemoveAll(runRoot)
	completeRoot := filepath.Join(runRoot, "complete")
	incompleteRoot := filepath.Join(runRoot, "incomplete")
	databasePath := filepath.Join(runRoot, "megad.sqlite3")
	db, err := store.Open(context.Background(), databasePath)
	if err != nil {
		return measurement{}, err
	}
	defer db.Close()
	secrets, err := store.OpenSecretStore(filepath.Join(runRoot, "secret.key"))
	if err != nil {
		return measurement{}, err
	}
	service, err := settings.NewService(db)
	if err != nil {
		return measurement{}, err
	}
	manager, err := download.NewManager(download.Config{
		DB:                 db,
		Secrets:            secrets,
		Mega:               mega.NewClient(&http.Client{Timeout: 30 * time.Second}, fixture.baseURL),
		Settings:           service,
		CheckpointInterval: 2 * time.Second,
		CheckpointBytes:    256 << 20,
		WorkersPerFile:     workers,
		MaxActiveFiles:     1,
		MaxGlobalWorkers:   workers,
		ReadIdleTimeout:    90 * time.Second,
	})
	if err != nil {
		return measurement{}, err
	}
	defer manager.Close()

	jobID, err := insertBenchmarkJob(db, secrets, fixture, completeRoot, incompleteRoot, size)
	if err != nil {
		return measurement{}, err
	}
	baselineStats := db.Stats()
	startTime := time.Now()
	startCPU := processCPUTicks()
	var maxRSS, maxFDs, maxGoroutines int64
	var maxOverhead int64

	done := make(chan error, 1)
	go func() { done <- manager.RunJob(context.Background(), jobID) }()
	sample := func() {
		if value := processRSSBytes(); value > atomic.LoadInt64(&maxRSS) {
			atomic.StoreInt64(&maxRSS, value)
		}
		if value := int64(processFDCount()); value > atomic.LoadInt64(&maxFDs) {
			atomic.StoreInt64(&maxFDs, value)
		}
		if value := int64(runtime.NumGoroutine()); value > atomic.LoadInt64(&maxGoroutines) {
			atomic.StoreInt64(&maxGoroutines, value)
		}
		// The physical file may contain ranges written after the last SQLite
		// checkpoint. Count all bytes that reached the writer, not only the
		// durable bitmap, when measuring overhead beyond payload data.
		committed := manager.Speed(jobID).TotalBytes
		if overhead := allocatedDiskBytes(runRoot) - committed; overhead > atomic.LoadInt64(&maxOverhead) {
			atomic.StoreInt64(&maxOverhead, overhead)
		}
	}
	sample()
	ticker := time.NewTicker(100 * time.Millisecond)
	var transferErr error
	running := true
	for running {
		select {
		case transferErr = <-done:
			running = false
		case <-ticker.C:
			sample()
		}
	}
	ticker.Stop()
	sample()
	if transferErr != nil {
		return measurement{}, transferErr
	}
	elapsed := time.Since(startTime)
	if elapsed <= 0 {
		elapsed = time.Nanosecond
	}
	endCPU := processCPUTicks()
	stats := db.Stats()
	writeTransactions := stats.WriteTransactions - baselineStats.WriteTransactions
	checkpointTransactions := stats.CheckpointTransactions - baselineStats.CheckpointTransactions
	cpuSeconds := float64(endCPU-startCPU) / 100.0
	wallSeconds := elapsed.Seconds()
	return measurement{
		Profile:                   current.Name,
		TargetCPUs:                current.CPUs,
		Workers:                   workers,
		PayloadBytes:              size,
		DurationSeconds:           wallSeconds,
		ThroughputMiBPerSecond:    float64(size) / (1 << 20) / wallSeconds,
		RSSMiBMax:                 float64(atomic.LoadInt64(&maxRSS)) / (1 << 20),
		CPUPercentOneCore:         cpuSeconds / wallSeconds * 100,
		CPUPercentOfTargetProfile: cpuSeconds / (wallSeconds * float64(current.CPUs)) * 100,
		OpenFDsMax:                int(atomic.LoadInt64(&maxFDs)),
		GoroutinesMax:             int(atomic.LoadInt64(&maxGoroutines)),
		SQLiteWriteTxPerSecond:    float64(writeTransactions) / wallSeconds,
		SQLiteCheckpointPerSecond: float64(checkpointTransactions) / wallSeconds,
		TempDiskOverheadMiBMax:    float64(maxInt64(0, atomic.LoadInt64(&maxOverhead))) / (1 << 20),
		ConstraintNote:            constraintNote(),
	}, nil
}

func constraintNote() string {
	if value := strings.TrimSpace(os.Getenv("MEGADW_RESOURCE_CONSTRAINT")); value != "" {
		return value
	}
	return "CPU constrained with GOMAXPROCS only; no cgroup memory limit is assumed by this process."
}

func insertBenchmarkJob(db *store.DB, secrets *store.SecretStore, fixture fixtureInfo, completeRoot, incompleteRoot string, size int64) (string, error) {
	client := mega.NewClient(&http.Client{Timeout: 30 * time.Second}, fixture.baseURL)
	job, err := client.ResolveLink(context.Background(), fixture.link, "")
	if err != nil {
		return "", err
	}
	if len(job.Files) != 1 {
		return "", fmt.Errorf("benchmark fixture returned %d files", len(job.Files))
	}
	file := job.Files[0]
	link, err := mega.ParseLink(fixture.link)
	if err != nil {
		return "", err
	}
	jobID := fmt.Sprintf("resource-%d", time.Now().UnixNano())
	sourceKey, err := secrets.Encrypt([]byte(link.Key))
	if err != nil {
		return "", err
	}
	fileKey, err := secrets.Encrypt(file.FileKey)
	if err != nil {
		return "", err
	}
	payloadURL, err := secrets.Encrypt([]byte(file.PayloadURL))
	if err != nil {
		return "", err
	}
	planner, err := download.NewSegmentPlanner(size, segmentSize)
	if err != nil {
		return "", err
	}
	_, err = db.InsertDownloadJob(context.Background(), store.DownloadJobInput{
		ID:                  jobID,
		SourceKind:          string(job.Kind),
		SourceHandle:        link.Handle,
		SourceKeyCiphertext: sourceKey,
		DisplayName:         job.DisplayName,
		TotalBytes:          size,
		CompleteRoot:        completeRoot,
		IncompleteRoot:      incompleteRoot,
		State:               string(download.JobReady),
		Files: []store.DownloadFileInput{{
			ID:                   jobID + "-file",
			RemoteNodeID:         file.NodeID,
			RemotePath:           file.RelativePath,
			FinalRelativePath:    file.RelativePath,
			SizeBytes:            size,
			SegmentSizeBytes:     segmentSize,
			SegmentCount:         planner.Count,
			FileKeyCiphertext:    fileKey,
			PayloadURLCiphertext: payloadURL,
			State:                string(download.FilePending),
		}},
	})
	return jobID, err
}

func startFixture(binary string, size int64, delay time.Duration) (fixtureInfo, func(), error) {
	command := exec.Command(binary, "-size", strconv.FormatInt(size, 10), "-delay", delay.String())
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fixtureInfo{}, func() {}, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return fixtureInfo{}, func() {}, err
	}
	stop := func() {
		if command.Process != nil {
			_ = command.Process.Signal(syscall.SIGTERM)
		}
		_ = command.Wait()
	}
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			lines <- scanner.Text()
			return
		}
		lines <- ""
	}()
	select {
	case line := <-lines:
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			stop()
			return fixtureInfo{}, func() {}, fmt.Errorf("fixture emitted invalid endpoint line %q", line)
		}
		return fixtureInfo{baseURL: parts[0], link: parts[1]}, stop, nil
	case <-time.After(10 * time.Second):
		stop()
		return fixtureInfo{}, func() {}, errors.New("timed out waiting for benchmark fixture")
	}
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
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return value * 1024
	}
	return 0
}

func processFDCount() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(entries)
}

func processCPUTicks() int64 {
	raw, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	closing := strings.LastIndexByte(string(raw), ')')
	if closing < 0 || closing+2 >= len(raw) {
		return 0
	}
	fields := strings.Fields(string(raw)[closing+2:])
	if len(fields) < 13 {
		return 0
	}
	user, userErr := strconv.ParseInt(fields[11], 10, 64)
	system, systemErr := strconv.ParseInt(fields[12], 10, 64)
	if userErr != nil || systemErr != nil {
		return 0
	}
	return user + system
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

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
