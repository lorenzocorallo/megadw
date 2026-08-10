package events

import (
	"context"
	"sync"
	"time"
)

const (
	defaultSubscriberQueue = 4
	maxBlockedFlushes      = 4
)

// Bus is a non-blocking fan-out bus. Publish never waits for an SSE client;
// each subscriber owns a bounded coalescer and output queue.
type Bus struct {
	mu         sync.Mutex
	subs       map[*subscriber]struct{}
	closed     bool
	queueSize  int
	maxPending int
	flushEvery time.Duration
}

type subscriber struct {
	bus       *Bus
	stream    chan Event
	stop      chan struct{}
	stopOnce  sync.Once
	done      chan struct{}
	mu        sync.Mutex
	coalescer *Coalescer
	blocked   int
}

// NewBus creates a bounded event bus with production-safe defaults.
func NewBus() *Bus {
	return &Bus{
		subs:       make(map[*subscriber]struct{}),
		queueSize:  defaultSubscriberQueue,
		maxPending: defaultPendingEvents,
		flushEvery: coalesceInterval,
	}
}

// Subscribe adds a client. The optional context is convenient for HTTP
// handlers and causes the subscription to be removed when it is cancelled.
func (b *Bus) Subscribe(ctx ...context.Context) *Subscription {
	if b == nil {
		return &Subscription{ch: closedEvents(), closed: closedSignal()}
	}
	s := &subscriber{
		bus:       b,
		stream:    make(chan Event, b.queueSize),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		coalescer: NewCoalescer(b.maxPending),
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(s.stop)
		close(s.done)
		close(s.stream)
		return &Subscription{ch: s.stream, closed: s.done, close: func() {}}
	}
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	if len(ctx) > 0 && ctx[0] != nil {
		go func() {
			select {
			case <-ctx[0].Done():
				s.close()
			case <-s.done:
			}
		}()
	}
	go s.run()
	return &Subscription{ch: s.stream, closed: s.done, close: s.close}
}

// Publish queues an event for all current subscribers without waiting. False
// means the event was rejected by a closed/full subscriber; publishers are
// expected to ignore that result because reconnect/refetch repairs snapshots.
func (b *Bus) Publish(event Event) bool {
	if b == nil || event.Name == "" {
		return false
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return false
	}
	accepted := true
	for sub := range b.subs {
		if !sub.enqueue(event) {
			accepted = false
			sub.close()
		}
	}
	b.mu.Unlock()
	return accepted
}

// Close disconnects all subscribers and prevents new subscriptions.
func (b *Bus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*subscriber, 0, len(b.subs))
	for sub := range b.subs {
		subs = append(subs, sub)
	}
	b.mu.Unlock()
	for _, sub := range subs {
		sub.close()
	}
}

func (s *subscriber) enqueue(event Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stop:
		return false
	default:
	}
	return s.coalescer.Add(event)
}

func (s *subscriber) run() {
	ticker := time.NewTicker(s.bus.flushEvery)
	defer ticker.Stop()
	defer close(s.stream)
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			s.remove()
			return
		case <-ticker.C:
			if !s.flush() {
				s.remove()
				return
			}
		}
	}
}

func (s *subscriber) flush() bool {
	s.mu.Lock()
	events := s.coalescer.Flush()
	if len(events) == 0 {
		s.blocked = 0
		s.mu.Unlock()
		return true
	}
	for _, event := range events {
		select {
		case s.stream <- event:
			s.blocked = 0
		default:
			s.blocked++
			// Events removed from the coalescer are intentionally not put back:
			// a client that cannot drain is going to be disconnected and must
			// refetch on reconnect.
			if s.blocked >= maxBlockedFlushes {
				s.mu.Unlock()
				return false
			}
		}
	}
	s.mu.Unlock()
	return true
}

func (s *subscriber) close() {
	s.stopOnce.Do(func() {
		close(s.stop)
	})
}

func (s *subscriber) remove() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
}

func closedEvents() chan Event {
	ch := make(chan Event)
	close(ch)
	return ch
}

func closedSignal() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
