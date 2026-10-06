// Package devsched serializes requests to a device that allows only one
// in-flight request at a time. Transaction IDs are recycled on a ring;
// a released ID stays in quarantine for a configurable isolation period
// so a late response from a previous request can never be mistaken for
// the answer to a new request that reused the same ID.
package devsched

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrTimeout is returned when the device does not answer in time.
	ErrTimeout = errors.New("devsched: request timed out")
	// ErrClosed is returned by queued or new requests after Close.
	ErrClosed = errors.New("devsched: scheduler closed")
)

// Transport delivers one request to the device. Send must not block;
// responses are reported back through Scheduler.HandleResponse.
type Transport interface {
	Send(txnID uint32, payload []byte) error
}

// Stats counts scheduler events. All fields are monotonic.
type Stats struct {
	Completed      int
	Timeouts       int
	SendErrors     int
	Cancelled      int
	StaleResponses int
}

// Scheduler keeps at most one request in flight and queues the rest.
type Scheduler struct {
	transport Transport
	clock     Clock
	timeout   time.Duration
	isolation time.Duration
	idSpace   int

	mu       sync.Mutex
	queue    []*slot
	inflight *slot
	nextID   uint32
	used     []bool      // used[id]: id was allocated at least once
	released []time.Time // released[id]: last alloc/release time of id
	wake     Timer       // pending quarantine-expiry wakeup
	closed   bool
	stats    Stats

	wg sync.WaitGroup // tracks the in-flight slot for Close
}

type slot struct {
	payload    []byte
	ctx        context.Context
	result     chan outcome
	txnID      uint32
	timer      Timer
	completed  bool
	callerGone bool // ctx cancelled while in flight; discard the answer
}

type outcome struct {
	resp []byte
	err  error
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithClock sets the clock; defaults to RealClock().
func WithClock(c Clock) Option { return func(s *Scheduler) { s.clock = c } }

// WithTimeout sets the per-request in-flight timeout; default 1s.
func WithTimeout(d time.Duration) Option { return func(s *Scheduler) { s.timeout = d } }

// WithIsolation sets the quarantine period before a transaction ID may be
// reused. It must exceed the longest delay with which the device can still
// deliver a response after the request timed out. Default 5s.
func WithIsolation(d time.Duration) Option {
	return func(s *Scheduler) { s.isolation = d }
}

// WithTxnIDSpace sets the number of transaction IDs before the ID wraps
// around; default 256.
func WithTxnIDSpace(n int) Option { return func(s *Scheduler) { s.idSpace = n } }

// New creates a Scheduler. It panics on invalid configuration.
func New(transport Transport, opts ...Option) *Scheduler {
	s := &Scheduler{
		transport: transport,
		clock:     RealClock(),
		timeout:   time.Second,
		isolation: 5 * time.Second,
		idSpace:   256,
	}
	for _, opt := range opts {
		opt(s)
	}
	if transport == nil {
		panic("devsched: nil transport")
	}
	if s.timeout <= 0 {
		panic("devsched: timeout must be positive")
	}
	if s.isolation < 0 {
		panic("devsched: isolation must not be negative")
	}
	if s.idSpace < 1 {
		panic("devsched: transaction ID space must be at least 1")
	}
	s.used = make([]bool, s.idSpace)
	s.released = make([]time.Time, s.idSpace)
	return s
}

// Submit queues payload and blocks until the device answers, the request
// times out, ctx is cancelled, or the scheduler is closed. Cancelling ctx
// while the request is still queued removes it without disturbing the
// in-flight request; cancelling while in flight detaches the caller but
// the device slot stays occupied until the answer or the timeout arrives.
func (s *Scheduler) Submit(ctx context.Context, payload []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sl := &slot{payload: payload, ctx: ctx, result: make(chan outcome, 1)}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	s.queue = append(s.queue, sl)
	s.dispatchLocked()
	s.mu.Unlock()

	select {
	case o := <-sl.result:
		return o.resp, o.err
	case <-ctx.Done():
		s.mu.Lock()
		if sl.completed {
			// The real outcome raced with the cancellation; prefer it.
			o := <-sl.result
			s.mu.Unlock()
			return o.resp, o.err
		}
		sl.completed = true
		if s.inflight == sl {
			sl.callerGone = true
		} else {
			s.removeFromQueueLocked(sl)
			s.stats.Cancelled++
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

// HandleResponse reports a device response. It completes the in-flight
// request only if the transaction ID matches; any other response is a
// stale (late or foreign) answer and is dropped. It reports whether the
// response was accepted.
func (s *Scheduler) HandleResponse(txnID uint32, payload []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.inflight
	if sl == nil || sl.txnID != txnID {
		s.stats.StaleResponses++
		return false
	}
	s.stats.Completed++
	s.releaseLocked(sl, outcome{resp: payload})
	return true
}

// Close stops accepting new requests, fails every queued request with
// ErrClosed, and blocks until the in-flight request drains (response or
// timeout). It is safe to call more than once.
func (s *Scheduler) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	queued := s.queue
	s.queue = nil
	for _, sl := range queued {
		s.completeLocked(sl, outcome{err: ErrClosed})
	}
	if s.wake != nil {
		s.wake.Stop()
		s.wake = nil
	}
	s.mu.Unlock()

	s.wg.Wait()
	return nil
}

// Stats returns a snapshot of the event counters.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Queued reports how many requests are waiting in the queue.
func (s *Scheduler) Queued() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// InFlight reports whether a request is currently on the device.
func (s *Scheduler) InFlight() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight != nil
}

// dispatchLocked sends queued requests while the device is idle and a
// transaction ID outside its isolation period is available.
func (s *Scheduler) dispatchLocked() {
	for !s.closed && s.inflight == nil && len(s.queue) > 0 {
		sl := s.queue[0]
		if err := sl.ctx.Err(); err != nil {
			s.queue = s.queue[1:]
			s.stats.Cancelled++
			s.completeLocked(sl, outcome{err: err})
			continue
		}
		now := s.clock.Now()
		id, ok := s.allocIDLocked(now)
		if !ok {
			s.armWakeLocked(now)
			return
		}
		s.queue = s.queue[1:]
		sl.txnID = id
		s.inflight = sl
		s.used[id] = true
		s.released[id] = now // occupied from now on; also blocks re-allocation
		if err := s.transport.Send(id, sl.payload); err != nil {
			s.inflight = nil
			s.stats.SendErrors++
			s.completeLocked(sl, outcome{err: err})
			continue
		}
		s.wg.Add(1)
		sl.timer = s.clock.AfterFunc(s.timeout, func() { s.onTimeout(sl) })
	}
}

// allocIDLocked picks the next ID in ring order whose isolation period has
// elapsed. The in-flight ID is never eligible because its release timestamp
// was bumped at allocation time.
func (s *Scheduler) allocIDLocked(now time.Time) (uint32, bool) {
	for i := 0; i < s.idSpace; i++ {
		id := (s.nextID + uint32(i)) % uint32(s.idSpace)
		if !s.used[id] || now.Sub(s.released[id]) >= s.isolation {
			s.nextID = (id + 1) % uint32(s.idSpace)
			return id, true
		}
	}
	return 0, false
}

// armWakeLocked schedules a wakeup for the moment the earliest quarantined
// ID becomes reusable.
func (s *Scheduler) armWakeLocked(now time.Time) {
	var earliest time.Time
	for id := range s.released {
		if !s.used[id] {
			continue
		}
		exp := s.released[id].Add(s.isolation)
		if exp.After(now) && (earliest.IsZero() || exp.Before(earliest)) {
			earliest = exp
		}
	}
	if earliest.IsZero() {
		return
	}
	if s.wake != nil {
		s.wake.Stop()
	}
	s.wake = s.clock.AfterFunc(earliest.Sub(now), s.onWake)
}

func (s *Scheduler) onWake() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wake = nil
	s.dispatchLocked()
}

func (s *Scheduler) onTimeout(sl *slot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight != sl {
		return
	}
	s.stats.Timeouts++
	s.releaseLocked(sl, outcome{err: ErrTimeout})
}

// releaseLocked retires the in-flight slot, starts its ID's isolation
// period, and dispatches the next queued request.
func (s *Scheduler) releaseLocked(sl *slot, o outcome) {
	if sl.timer != nil {
		sl.timer.Stop()
	}
	s.released[sl.txnID] = s.clock.Now()
	s.inflight = nil
	s.wg.Done()
	s.completeLocked(sl, o)
	s.dispatchLocked()
}

func (s *Scheduler) completeLocked(sl *slot, o outcome) {
	if sl.completed {
		return
	}
	sl.completed = true
	if !sl.callerGone {
		sl.result <- o
	}
}

func (s *Scheduler) removeFromQueueLocked(sl *slot) {
	for i, q := range s.queue {
		if q == sl {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}
