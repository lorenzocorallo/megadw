package download

import (
	"context"
	"testing"
	"time"
)

func TestBandwidthLimiterCanBeCancelledWhileWaiting(t *testing.T) {
	limiter := NewBandwidthLimiter(1)
	if err := limiter.WaitN(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := limiter.WaitN(ctx, 1); err == nil {
		t.Fatal("limiter wait completed after cancellation deadline")
	}
}

func TestBandwidthLimiterZeroRateIsUnlimited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := NewBandwidthLimiter(0).WaitN(ctx, 4<<20); err != nil {
		t.Fatal(err)
	}
}

func TestSpeedMeterIsBoundedAndRaceSafe(t *testing.T) {
	meter := NewSpeedMeter(time.Second)
	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func() {
			for count := 0; count < 1_000; count++ {
				meter.Add(512)
				_ = meter.Snapshot()
			}
			done <- struct{}{}
		}()
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
	snapshot := meter.Snapshot()
	if snapshot.TotalBytes != 8*1_000*512 {
		t.Fatalf("total bytes = %d", snapshot.TotalBytes)
	}
	if len(meter.samples) > maxSpeedSampleBucket {
		t.Fatalf("speed buckets = %d, want <= %d", len(meter.samples), maxSpeedSampleBucket)
	}
}
