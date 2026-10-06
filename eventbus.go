// Package eventbus implements a sequenced in-process event bus.
//
// A subscription first replays the retained history after a caller-chosen
// sequence number, then switches over to the live stream. The bus keeps
// accepting publishes while a replay is in flight; the replay/live junction
// is guaranteed to be gap-free and duplicate-free because every delivery is
// sourced from the same append-only history by sequence number.
//
// Each subscription has a fixed backlog quota: a buffered channel of quota
// events. A subscriber whose buffer is full when a new event is published
// is disconnected explicitly and can read the last delivered sequence
// number to resubscribe without loss.
package eventbus

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrBacklogExceeded is reported via Subscription.Err when a subscriber's
// backlog buffer was full at publish time, i.e. it fell the full quota
// behind the delivery pump.
var ErrBacklogExceeded = errors.New("eventbus: subscriber backlog quota exceeded")

// Event is a single bus message. Seq is assigned by the bus, starts at 1
// and increases by one for every published event, with no gaps.
type Event struct {
	Seq  uint64
	Data interface{}
}

// Bus is an append-only sequenced event log with fan-out subscriptions.
type Bus struct {
	mu      sync.Mutex
	seq     uint64
	history []Event
	notify  chan struct{} // closed and replaced on every publish
	subs    map[*Subscription]struct{}
	quota   int
}

// New returns a Bus whose subscriptions may buffer up to quota undelivered
// events before being disconnected as slow.
func New(quota int) *Bus {
	if quota < 1 {
		quota = 1
	}
	return &Bus{
		notify: make(chan struct{}),
		subs:   make(map[*Subscription]struct{}),
		quota:  quota,
	}
}

// Publish appends data to the history, wakes live subscribers and
// disconnects subscribers whose backlog buffer is full. It returns the
// sequence number assigned to the event.
func (b *Bus) Publish(data interface{}) uint64 {
	b.mu.Lock()
	b.seq++
	seq := b.seq
	b.history = append(b.history, Event{Seq: seq, Data: data})
	close(b.notify)
	b.notify = make(chan struct{})
	for s := range b.subs {
		if len(s.ch) == cap(s.ch) {
			s.fail()
		}
	}
	b.mu.Unlock()
	return seq
}

// Len returns the number of events published so far, i.e. the newest
// sequence number.
func (b *Bus) Len() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// History returns a copy of the retained events with Seq in (after, to].
// A to of 0 means "up to the newest event". The result is a contiguous
// slice with no gaps.
func (b *Bus) History(after, to uint64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if to == 0 || to > b.seq {
		to = b.seq
	}
	if after >= to {
		return nil
	}
	out := make([]Event, to-after)
	copy(out, b.history[after:to])
	return out
}

// SubscriberCount reports how many subscriptions are still active.
func (b *Bus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Subscribe registers a subscription that first replays every retained
// event with Seq > after (in order) and then delivers live events as they
// are published. Events published while the replay is in flight are
// delivered exactly once, after the replayed prefix.
func (b *Bus) Subscribe(after uint64) *Subscription {
	s := &Subscription{
		ch:     make(chan Event, b.quota),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	go b.pump(s, after+1)
	return s
}

// pump is the single delivery loop of a subscription. It walks the shared
// history by sequence number, so replay and live delivery use one code
// path and the junction cannot skip or repeat an event.
func (b *Bus) pump(s *Subscription, next uint64) {
	defer func() {
		atomic.StoreUint64(&s.last, next-1)
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
		close(s.ch)
		close(s.exited)
	}()
	for {
		b.mu.Lock()
		if next <= b.seq {
			ev := b.history[next-1]
			b.mu.Unlock()
			select {
			case s.ch <- ev:
				next++
			case <-s.done:
				return
			}
		} else {
			notify := b.notify
			b.mu.Unlock()
			select {
			case <-notify:
			case <-s.done:
				return
			}
		}
	}
}

// Subscription is one consumer's view of the bus.
type Subscription struct {
	ch     chan Event
	done   chan struct{}
	exited chan struct{}
	once   sync.Once
	err    error
	last   uint64 // atomic; valid once C is closed
}

// C returns the channel of events, replayed history first, then live.
// The channel is closed when the subscription ends for any reason;
// buffered events remain drainable after a disconnect.
func (s *Subscription) C() <-chan Event { return s.ch }

// Cancel detaches the subscription. The pump goroutine exits, the event
// channel is closed and the bus drops its reference, releasing any
// backlog held for this subscriber.
func (s *Subscription) Cancel() {
	s.once.Do(func() { close(s.done) })
	<-s.exited
}

func (s *Subscription) fail() {
	s.once.Do(func() {
		s.err = ErrBacklogExceeded
		close(s.done)
	})
}

// Err reports why the subscription ended: ErrBacklogExceeded after a
// quota disconnect, nil after Cancel.
func (s *Subscription) Err() error {
	<-s.exited
	return s.err
}

// Last returns the sequence number of the last event delivered to the
// subscriber's buffer. It is valid once the event channel is closed and
// is the correct resubscription point after a disconnect.
func (s *Subscription) Last() uint64 {
	<-s.exited
	return atomic.LoadUint64(&s.last)
}
