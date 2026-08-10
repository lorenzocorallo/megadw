package events

import "time"

const (
	defaultPendingEvents = 128
	maxEventsPerSecond   = 4
	coalesceInterval     = time.Second / maxEventsPerSecond
)

// Coalescer keeps a bounded latest-value set. Progress-like events use the
// job as their key, so a busy transfer replaces stale progress rather than
// growing memory or making a browser consume every segment completion.
type Coalescer struct {
	maxPending int
	pending    map[string]Event
	order      []string
}

func NewCoalescer(maxPending int) *Coalescer {
	if maxPending < 1 {
		maxPending = defaultPendingEvents
	}
	return &Coalescer{maxPending: maxPending, pending: make(map[string]Event)}
}

func (c *Coalescer) Add(event Event) bool {
	if c == nil || event.Name == "" {
		return false
	}
	key := eventKey(event)
	if previous, ok := c.pending[key]; ok {
		c.pending[key] = merge(previous, event)
		return true
	}
	if len(c.pending) >= c.maxPending {
		if !isCoalescible(event) {
			return false
		}
		// Drop the oldest replaceable item. The next reconnect/refetch is the
		// source of truth, while the live stream remains strictly bounded.
		for index, oldest := range c.order {
			if isCoalescible(c.pending[oldest]) {
				delete(c.pending, oldest)
				c.order = append(c.order[:index], c.order[index+1:]...)
				break
			}
		}
		if len(c.pending) >= c.maxPending {
			return false
		}
	}
	c.pending[key] = event
	c.order = append(c.order, key)
	return true
}

func (c *Coalescer) Flush() []Event {
	if c == nil || len(c.pending) == 0 {
		return nil
	}
	result := make([]Event, 0, len(c.pending))
	for _, key := range c.order {
		if event, ok := c.pending[key]; ok {
			result = append(result, event)
		}
	}
	c.pending = make(map[string]Event)
	c.order = c.order[:0]
	return result
}

func eventKey(event Event) string {
	if isCoalescible(event) {
		if event.JobID != "" {
			return "job:" + event.JobID
		}
		if event.FileID != "" {
			return "file:" + event.FileID
		}
	}
	return event.Name + ":" + event.JobID + ":" + event.FileID
}

func isCoalescible(event Event) bool {
	switch event.Name {
	case JobUpdated, FileUpdated, SpeedUpdated, QueueUpdated:
		return event.JobID != "" || event.FileID != ""
	default:
		return false
	}
}

func merge(previous, latest Event) Event {
	merged := latest
	if previous.Timestamp.After(merged.Timestamp) {
		merged.Timestamp = previous.Timestamp
	}
	if len(previous.Data) != 0 || len(latest.Data) != 0 {
		merged.Data = make(map[string]any, len(previous.Data)+len(latest.Data))
		for key, value := range previous.Data {
			merged.Data[key] = value
		}
		for key, value := range latest.Data {
			merged.Data[key] = value
		}
	}
	if merged.JobID == "" {
		merged.JobID = previous.JobID
	}
	if merged.FileID == "" {
		merged.FileID = previous.FileID
	}
	return merged
}
