// Package eventbus provides an in-process event bus with replayable
// history and bounded per-subscriber backlogs.
//
// A subscriber first replays all events after a caller-supplied sequence
// number, then seamlessly joins the live stream. The replay snapshot and
// the subscriber registration happen under the same lock, so events
// published during replay are neither lost nor duplicated at the
// replay/live boundary.
//
// Each subscriber owns a fixed-size backlog. A slow subscriber that
// exceeds the quota is explicitly disconnected; it can inspect the last
// delivered sequence number and resubscribe from it without gaps or
// duplicates.
package eventbus

import "sync"

// Event is a single published message. Seq is assigned by the bus,
// starts at 1 and increases monotonically without gaps.
type Event struct {
	Seq  int64
	Data interface{}
}

// Bus is a concurrency-safe event bus. The zero value is not usable;
// create one with New.
type Bus struct {
	mu      sync.Mutex
	history []Event
	subs    map[*subscriber]struct{}
	quota   int
	backlog int // total events currently buffered across all subscribers
}

// New returns a Bus whose per-subscriber backlog quota is quota events.
// A subscriber is disconnected once a delivery would exceed the quota.
func New(quota int) *Bus {
	if quota < 1 {
		quota = 1
	}
	return &Bus{
		subs:  make(map[*subscriber]struct{}),
		quota: quota,
	}
}

// Publish appends data to the history and delivers it to every live
// subscriber. It returns the stored event, including its sequence number.
func (b *Bus) Publish(data interface{}) Event {
	b.mu.Lock()
	ev := Event{Seq: int64(len(b.history)) + 1, Data: data}
	b.history = append(b.history, ev)
	for s := range b.subs {
		b.enqueue(s, ev)
	}
	b.mu.Unlock()
	return ev
}

// Subscribe registers a new subscriber. All events with Seq > fromSeq
// already in the history are replayed first, followed by live events in
// publish order. Use fromSeq == 0 to receive the full history.
//
// If the replay alone exceeds the backlog quota, the subscription is
// disconnected immediately with LastDelivered set to the last event that
// still fit in the backlog.
func (b *Bus) Subscribe(fromSeq int64) *Subscription {
	s := &subscriber{ch: make(chan Event, b.quota)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	for _, ev := range b.history {
		if ev.Seq > fromSeq {
			b.enqueue(s, ev)
		}
	}
	b.mu.Unlock()
	return &Subscription{bus: b, sub: s}
}

// Snapshot returns a copy of the full event history.
func (b *Bus) Snapshot() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, len(b.history))
	copy(out, b.history)
	return out
}

// Len returns the number of events in the history.
func (b *Bus) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.history)
}

// Backlog returns the total number of events currently buffered in all
// subscriber backlogs.
func (b *Bus) Backlog() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.backlog
}

// Subscribers returns the number of live subscriptions.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// enqueue delivers ev to s, disconnecting s if its backlog is full.
// It must be called with b.mu held.
func (b *Bus) enqueue(s *subscriber, ev Event) {
	if len(s.ch) >= b.quota {
		b.disconnect(s, true)
		return
	}
	s.ch <- ev
	s.last = ev.Seq
	b.backlog++
}

// disconnect removes s and closes its channel. Buffered events remain
// readable after the close. dropped records whether the disconnection
// was caused by exceeding the backlog quota. It must be called with
// b.mu held.
func (b *Bus) disconnect(s *subscriber, dropped bool) {
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	b.backlog -= len(s.ch)
	s.dropped = dropped
	close(s.ch)
}

type subscriber struct {
	ch      chan Event
	last    int64 // last sequence number handed to the subscriber
	dropped bool
}

// Subscription is a handle to one subscriber's event stream.
type Subscription struct {
	bus  *Bus
	sub  *subscriber
	once sync.Once
}

// C returns the receive-only stream of events. It is closed when the
// subscription is cancelled or disconnected; buffered events remain
// readable after closure.
func (s *Subscription) C() <-chan Event {
	return s.sub.ch
}

// Cancel detaches the subscription from the bus and releases its
// backlog. It is idempotent and safe to call concurrently with Publish.
func (s *Subscription) Cancel() {
	s.once.Do(func() {
		s.bus.mu.Lock()
		s.bus.disconnect(s.sub, false)
		s.bus.mu.Unlock()
	})
}

// LastDelivered returns the sequence number of the most recent event
// handed to this subscription, or 0 if none has been delivered yet.
func (s *Subscription) LastDelivered() int64 {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.sub.last
}

// Dropped reports whether the subscription was disconnected for
// exceeding the backlog quota (as opposed to being cancelled).
func (s *Subscription) Dropped() bool {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.sub.dropped
}
