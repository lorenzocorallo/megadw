package download

import (
	"sync"
	"time"
)

const (
	defaultSpeedWindow   = 30 * time.Second
	speedSampleInterval  = 250 * time.Millisecond
	maxSpeedSampleBucket = 128
)

type speedSample struct {
	start time.Time
	bytes int64
}

// SpeedSnapshot is a bounded rolling throughput measurement. It is a value
// type so callers can safely expose it to API/SSE layers without sharing
// mutable state with transfer workers.
type SpeedSnapshot struct {
	BytesPerSecond float64       `json:"bytesPerSecond"`
	TotalBytes     int64         `json:"totalBytes"`
	Window         time.Duration `json:"window"`
}

// SpeedMeter aggregates transfer bytes into a fixed number of time buckets.
// It has no per-byte allocations and remains safe when all range workers
// report to the same job meter.
type SpeedMeter struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	samples []speedSample
	total   int64
}

// NewSpeedMeter creates a rolling meter. Invalid or zero windows use the
// product's 30-second detail-chart window.
func NewSpeedMeter(window time.Duration) *SpeedMeter {
	if window <= 0 {
		window = defaultSpeedWindow
	}
	if window < speedSampleInterval {
		window = speedSampleInterval
	}
	return &SpeedMeter{window: window, now: time.Now}
}

// Add records n transferred bytes. Negative values are ignored because a
// speed meter represents completed network work only.
func (m *SpeedMeter) Add(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.mu.Lock()
	now := m.currentTime()
	m.expireLocked(now)
	bucket := now.Truncate(speedSampleInterval)
	if len(m.samples) == 0 || !m.samples[len(m.samples)-1].start.Equal(bucket) {
		m.samples = append(m.samples, speedSample{start: bucket})
	}
	m.samples[len(m.samples)-1].bytes += n
	m.total += n
	m.trimCapacityLocked()
	m.mu.Unlock()
}

// Snapshot returns throughput over the samples retained by the rolling
// window. It intentionally reports zero after an idle window expires.
func (m *SpeedMeter) Snapshot() SpeedSnapshot {
	if m == nil {
		return SpeedSnapshot{}
	}
	m.mu.Lock()
	now := m.currentTime()
	m.expireLocked(now)
	var bytes int64
	for _, sample := range m.samples {
		bytes += sample.bytes
	}
	window := m.window
	if len(m.samples) > 0 {
		elapsed := now.Sub(m.samples[0].start)
		if elapsed < speedSampleInterval {
			elapsed = speedSampleInterval
		}
		if elapsed > 0 && elapsed < window {
			window = elapsed
		}
	}
	var rate float64
	if bytes > 0 && window > 0 {
		rate = float64(bytes) / window.Seconds()
	}
	total := m.total
	m.mu.Unlock()
	return SpeedSnapshot{BytesPerSecond: rate, TotalBytes: total, Window: window}
}

// BytesPerSecond is a convenience accessor for rate-only callers.
func (m *SpeedMeter) BytesPerSecond() float64 {
	return m.Snapshot().BytesPerSecond
}

// Reset clears the rolling and lifetime counters.
func (m *SpeedMeter) Reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.samples = nil
	m.total = 0
	m.mu.Unlock()
}

func (m *SpeedMeter) currentTime() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *SpeedMeter) expireLocked(now time.Time) {
	cutoff := now.Add(-m.window)
	first := 0
	for first < len(m.samples) && m.samples[first].start.Add(speedSampleInterval).Before(cutoff) {
		first++
	}
	if first > 0 {
		m.samples = append([]speedSample(nil), m.samples[first:]...)
	}
}

func (m *SpeedMeter) trimCapacityLocked() {
	if len(m.samples) <= maxSpeedSampleBucket {
		return
	}
	first := len(m.samples) - maxSpeedSampleBucket
	m.samples = append([]speedSample(nil), m.samples[first:]...)
}
