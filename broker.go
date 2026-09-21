// Package pubsub provides a small in-process, fan-out publish/subscribe
// broker built only on the Go standard library.
//
// Every subscriber owns an independent bounded queue. A slow subscriber
// never blocks or silently loses messages for any other subscriber: when a
// queue is full the publisher either blocks until space is available
// (OverflowBlock) or the subscriber's message is discarded explicitly and
// counted (OverflowDropNewest).
package pubsub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrClosed is returned by operations performed after the broker (or
	// the targeted subscription) has been closed.
	ErrClosed = errors.New("pubsub: broker closed")

	// ErrInvalidBuffer is returned when a non-positive buffer is requested.
	ErrInvalidBuffer = errors.New("pubsub: buffer must be greater than zero")
)

// OverflowPolicy selects what happens when a subscriber's queue is full.
type OverflowPolicy int

const (
	// OverflowBlock makes Publish wait for queue space (bounded by ctx).
	OverflowBlock OverflowPolicy = iota
	// OverflowDropNewest makes the full subscriber explicitly discard the
	// incoming message. The discard is counted and reported via OnDrop.
	OverflowDropNewest
)

func (p OverflowPolicy) String() string {
	switch p {
	case OverflowBlock:
		return "block"
	case OverflowDropNewest:
		return "drop-newest"
	default:
		return fmt.Sprintf("overflow(%d)", int(p))
	}
}

// Stats holds broker-wide delivery counters.
type Stats struct {
	Published int64 // fan-outs that completed (across all subscribers)
	Delivered int64 // messages enqueued into subscriber queues
	Dropped   int64 // messages explicitly discarded for a full queue
}

// SubscriberConfig configures a new subscription.
type SubscriberConfig struct {
	// Buffer is the capacity of the subscriber's bounded queue. Required > 0.
	Buffer int
	// Overflow selects the slow-consumer strategy. Defaults to OverflowBlock.
	Overflow OverflowPolicy
	// OnDrop, when set, is invoked synchronously for each message discarded
	// under OverflowDropNewest. It must return quickly.
	OnDrop func(msg any)
}

// Broker is a fan-out pub/sub broker. It is safe for concurrent use.
type Broker struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	closed bool

	published atomic.Int64
	delivered atomic.Int64
	dropped   atomic.Int64
}

// NewBroker creates an empty broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[*Subscription]struct{})}
}

// Subscribe registers a subscriber with an independent bounded queue.
func (b *Broker) Subscribe(cfg SubscriberConfig) (*Subscription, error) {
	if cfg.Buffer <= 0 {
		return nil, ErrInvalidBuffer
	}
	s := &Subscription{
		broker: b,
		cfg:    cfg,
		ch:     make(chan any, cfg.Buffer),
		done:   make(chan struct{}),
		abort:  make(chan struct{}),
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	b.subs[s] = struct{}{}
	return s, nil
}

// Publish fans msg out to every subscriber registered at call time.
//
// Subscribers are delivered concurrently from independent goroutines, so a
// slow subscriber cannot stall delivery to others. Under OverflowBlock a
// single subscriber may hold Publish until its queue has space or ctx is
// done. Under OverflowDropNewest a full queue discards that subscriber's
// message explicitly (counted, reported via OnDrop); Publish is unaffected.
// ErrClosed is returned once the broker is closing or closed.
func (b *Broker) Publish(ctx context.Context, msg any) error {
	if ctx == nil {
		ctx = context.Background()
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		// Registered before releasing the lock so shutdown/Unsubscribe
		// always waits for this delivery.
		s.inFlight.Add(1)
		subs = append(subs, s)
	}
	b.mu.Unlock()

	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)
	for _, s := range subs {
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			defer s.inFlight.Done()
			if err := s.deliver(ctx, msg); err != nil && !errors.Is(err, errDropped) {
				errsMu.Lock()
				errs = append(errs, err)
				errsMu.Unlock()
			}
		}(s)
	}
	wg.Wait()
	b.published.Add(1)

	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		// A concurrent shutdown aborted part of the fan-out: the message
		// was not committed to every target subscriber.
		return ErrClosed
	}
	return errors.Join(errs...)
}

// Subscription is one subscriber's independent bounded queue.
type Subscription struct {
	broker *Broker
	cfg    SubscriberConfig

	ch    chan any      // bounded message queue; never closed
	done  chan struct{} // closed after all delivery has quiesced
	abort chan struct{} // closed to unblock in-flight delivery

	// inFlight tracks fan-out goroutines currently delivering to this
	// subscription only, so Unsubscribe never waits on other subscribers.
	inFlight sync.WaitGroup

	stateMu sync.Mutex
	doneCl  bool
	abortCl bool
}

// errDropped marks an explicit, policy-driven discard. It is not a failure
// surfaced from Publish.
var errDropped = errors.New("pubsub: message dropped due to full queue")

// deliver enqueues one message according to the overflow policy.
func (s *Subscription) deliver(ctx context.Context, msg any) error {
	if s.cfg.Overflow == OverflowDropNewest {
		select {
		case s.ch <- msg:
			s.broker.delivered.Add(1)
			return nil
		case <-s.abort:
			return ErrClosed
		default:
			s.broker.dropped.Add(1)
			if s.cfg.OnDrop != nil {
				s.cfg.OnDrop(msg)
			}
			return errDropped
		}
	}

	select {
	case s.ch <- msg:
		s.broker.delivered.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.abort:
		return ErrClosed
	}
}

// C returns the receive channel backing the bounded queue. The channel is
// never closed; observe Done to learn when no more messages will arrive.
func (s *Subscription) C() <-chan any { return s.ch }

// Done is closed when no further messages will ever be delivered. Messages
// already committed to the queue remain readable from C (see Drain).
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Drain consumes and returns every message currently buffered. It is meant
// to be called after Done is closed.
func (s *Subscription) Drain() []any {
	var out []any
	for {
		select {
		case m := <-s.ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

func (s *Subscription) signalAbort() {
	s.stateMu.Lock()
	if !s.abortCl {
		s.abortCl = true
		close(s.abort)
	}
	s.stateMu.Unlock()
}

func (s *Subscription) signalDone() {
	s.stateMu.Lock()
	if !s.doneCl {
		s.doneCl = true
		close(s.done)
	}
	s.stateMu.Unlock()
}

// Unsubscribe removes the subscriber. It blocks until in-flight delivery to
// this subscriber has finished, after which committed messages stay readable
// from C and no new messages arrive. It is idempotent and never panics.
func (s *Subscription) Unsubscribe() {
	b := s.broker

	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()

	s.signalAbort()
	s.inFlight.Wait() // this subscription's sends only
	s.signalDone()
}

// Close shuts the broker down. New publishes and subscribes fail with
// ErrClosed. Deliveries blocked on full queues are aborted; messages already
// committed to queues are preserved and can be drained after each Done.
// Close is idempotent.
func (b *Broker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()

	// Phase 1: unblock every delivery waiting on a full queue.
	for _, s := range subs {
		s.signalAbort()
	}
	// Phase 2: wait for all in-flight sends to finish. After this point no
	// goroutine can ever send on a subscriber channel, so no send-on-closed
	// is possible and committed contents are stable.
	for _, s := range subs {
		s.inFlight.Wait()
	}
	// Phase 3: announce quiescence. Queue channels stay open with their
	// committed contents for draining.
	for _, s := range subs {
		s.signalDone()
	}
	return nil
}

// Stats returns a snapshot of the broker counters.
func (b *Broker) Stats() Stats {
	return Stats{
		Published: b.published.Load(),
		Delivered: b.delivered.Load(),
		Dropped:   b.dropped.Load(),
	}
}
