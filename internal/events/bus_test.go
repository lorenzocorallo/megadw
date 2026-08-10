package events

import (
	"fmt"
	"testing"
	"time"
)

func TestCoalescerKeepsLatestProgressBoundedByJob(t *testing.T) {
	coalescer := NewCoalescer(2)
	for index := 0; index < 100; index++ {
		if !coalescer.Add(Event{Name: SpeedUpdated, JobID: "job-1", Data: map[string]any{"value": index}}) {
			t.Fatal("progress event was rejected")
		}
	}
	items := coalescer.Flush()
	if len(items) != 1 {
		t.Fatalf("coalesced event count = %d, want 1", len(items))
	}
	if got := items[0].Data["value"]; got != 99 {
		t.Fatalf("latest progress = %v, want 99", got)
	}
}

func TestSlowSubscriberIsDisconnectedWithoutBlockingPublishers(t *testing.T) {
	bus := NewBus()
	subscription := bus.Subscribe()
	start := time.Now()
	for index := 0; index < 10_000; index++ {
		bus.Publish(Event{Name: SpeedUpdated, JobID: fmt.Sprintf("job-%d", index), Data: map[string]any{"value": index}})
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("publishing to a slow subscriber took %s", elapsed)
	}
	select {
	case <-subscription.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("slow subscriber was not disconnected")
	}
	bus.Close()
}

func TestSubscriberCoalescesAtMostOneEventPerJobPerInterval(t *testing.T) {
	bus := NewBus()
	subscription := bus.Subscribe()
	defer bus.Close()
	for index := 0; index < 20; index++ {
		bus.Publish(Event{Name: JobUpdated, JobID: "job-1", Data: map[string]any{"value": index}})
	}
	select {
	case event := <-subscription.Events():
		if event.Data["value"] != 19 {
			t.Fatalf("coalesced value = %v, want 19", event.Data["value"])
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced event was not delivered")
	}
	select {
	case event := <-subscription.Events():
		t.Fatalf("received a second event before a new publish: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}
