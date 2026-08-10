// Package events contains the bounded in-process event transport used by the
// download manager and the authenticated SSE endpoint.
package events

import "time"

const (
	JobUpdated      = "job.updated"
	FileUpdated     = "file.updated"
	SpeedUpdated    = "speed.updated"
	QueueUpdated    = "queue.updated"
	AccountUpdated  = "account.updated"
	SettingsUpdated = "settings.updated"
)

// Event is deliberately small and JSON-friendly. Data contains only public
// UI state; callers must never put credentials, link keys, or proxy secrets in
// it.
type Event struct {
	Name      string         `json:"name"`
	JobID     string         `json:"jobId,omitempty"`
	FileID    string         `json:"fileId,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// Subscription is one bounded, independently draining bus subscriber.
type Subscription struct {
	ch     chan Event
	close  func()
	closed chan struct{}
}

// Events returns the subscriber's bounded stream.
func (s *Subscription) Events() <-chan Event {
	if s == nil {
		return nil
	}
	return s.ch
}

// C is a short alias useful in select statements.
func (s *Subscription) C() <-chan Event { return s.Events() }

// Done is closed when the subscriber is disconnected by the bus.
func (s *Subscription) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.closed
}

// Close removes the subscription. It is safe to call more than once.
func (s *Subscription) Close() {
	if s != nil && s.close != nil {
		s.close()
	}
}
