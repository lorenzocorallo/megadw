package download

import (
	"context"
	"math"
	"sync"
	"time"
)

// BandwidthLimiter is a bounded token bucket used around network reads. A
// zero rate means unlimited; the limiter remains safe to share between all
// workers belonging to a process or one download job.
//
// The bucket deliberately has a bounded burst. This keeps a newly started
// transfer from allocating a large burst of memory or bypassing a small
// configured rate for an entire segment.
type BandwidthLimiter struct {
	mu     sync.Mutex
	rate   int64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewBandwidthLimiter creates a token bucket. A non-positive rate is treated
// as unlimited so callers can construct one directly from the settings value.
func NewBandwidthLimiter(rate int64) *BandwidthLimiter {
	if rate < 0 {
		rate = 0
	}
	burst := float64(rate)
	if burst < 1 {
		burst = 1
	}
	// A worker never asks for more than the transfer buffer in one read, and a
	// one MiB cap keeps the burst bounded even for very high limits.
	if burst > 1<<20 {
		burst = 1 << 20
	}
	return &BandwidthLimiter{
		rate:   rate,
		burst:  burst,
		tokens: burst,
		last:   time.Now(),
		now:    time.Now,
	}
}

// NewTokenBucketLimiter is an explicit alias for callers that prefer the
// implementation name.
func NewTokenBucketLimiter(rate int64) *BandwidthLimiter {
	return NewBandwidthLimiter(rate)
}

// Rate returns the configured bytes-per-second rate. Zero means unlimited.
func (l *BandwidthLimiter) Rate() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	rate := l.rate
	l.mu.Unlock()
	return rate
}

// SetRate changes the rate without replacing the limiter shared by workers.
// It is useful when a settings update applies to future reads.
func (l *BandwidthLimiter) SetRate(rate int64) {
	if l == nil {
		return
	}
	if rate < 0 {
		rate = 0
	}
	l.mu.Lock()
	l.refillLocked(l.currentTime())
	l.rate = rate
	l.burst = float64(rate)
	if l.burst < 1 {
		l.burst = 1
	}
	if l.burst > 1<<20 {
		l.burst = 1 << 20
	}
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.mu.Unlock()
}

// WaitN waits until n bytes can be consumed from the bucket or ctx is
// cancelled. It never sleeps while holding the bucket mutex.
func (l *BandwidthLimiter) WaitN(ctx context.Context, n int64) error {
	if n <= 0 || l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	remaining := n
	for remaining > 0 {
		l.mu.Lock()
		now := l.currentTime()
		l.refillLocked(now)
		if l.rate <= 0 {
			l.mu.Unlock()
			return nil
		}
		request := math.Min(l.burst, float64(remaining))
		if l.tokens >= request {
			l.tokens -= request
			remaining -= int64(request)
			l.mu.Unlock()
			continue
		}
		deficit := request - l.tokens
		wait := time.Duration(deficit / float64(l.rate) * float64(time.Second))
		if wait < time.Microsecond {
			wait = time.Microsecond
		}
		l.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

// Wait is the short form used by transfer code.
func (l *BandwidthLimiter) Wait(ctx context.Context, n int64) error {
	return l.WaitN(ctx, n)
}

func (l *BandwidthLimiter) currentTime() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *BandwidthLimiter) refillLocked(now time.Time) {
	if l.last.IsZero() {
		l.last = now
		return
	}
	if now.Before(l.last) {
		l.last = now
		return
	}
	if l.rate > 0 {
		l.tokens += now.Sub(l.last).Seconds() * float64(l.rate)
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
	}
	l.last = now
}
