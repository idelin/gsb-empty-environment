// Package txnsched serializes requests to a device that allows only one
// in-flight request at a time and whose responses may arrive after the
// request has already timed out.
//
// Transaction numbers are drawn from a small, wrapping space. To keep a
// late response from an old exchange from being mistaken for the answer
// to a newer request, every transaction number enters an isolation
// (quarantine) period when its exchange ends. A number cannot be reused
// while isolated, so for correctness the isolation period must be at
// least the maximum time a response can arrive late.
package txnsched

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrTimeout is delivered when the device does not answer in time.
	ErrTimeout = errors.New("txnsched: response timeout")
	// ErrClosed is delivered to requests submitted after Close.
	ErrClosed = errors.New("txnsched: scheduler closed")
	// ErrShutdown is delivered to queued requests drained by Close.
	ErrShutdown = errors.New("txnsched: drained by shutdown")
	// ErrQueueFull is delivered when the queue is at capacity.
	ErrQueueFull = errors.New("txnsched: queue full")
)

// Config tunes a Scheduler.
type Config struct {
	// TxnSpaceSize is the number of distinct transaction numbers
	// (e.g. 256 for a one-byte field). Must be >= 1.
	TxnSpaceSize int
	// Isolation is the quarantine period a transaction number spends
	// unused after its exchange ends. It must be at least the maximum
	// lateness of a device response, or a late response could collide
	// with a reused number.
	Isolation time.Duration
	// Timeout is how long the scheduler waits for a response before
	// failing the in-flight request.
	Timeout time.Duration
	// QueueSize bounds the number of queued (not yet sent) requests.
	// <= 0 means unbounded.
	QueueSize int
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.TxnSpaceSize <= 0 {
		out.TxnSpaceSize = 256
	}
	if out.Timeout <= 0 {
		out.Timeout = 5 * time.Second
	}
	return out
}

// Transport sends one request to the device. Implementations must be
// safe to call from the scheduler's event loop (i.e. not block).
type Transport interface {
	Send(txn int, payload []byte) error
}

// Response is a completed device reply.
type Response struct {
	Txn     int
	Payload []byte
}

// Result is the terminal outcome of a submitted request.
type Result struct {
	Resp Response
	Err  error
}

// Future is the handle for a submitted request. Exactly one Result is
// ever delivered.
type Future struct {
	ch   chan Result
	once sync.Once
}

func newFuture() *Future {
	return &Future{ch: make(chan Result, 1)}
}

func (f *Future) complete(r Result) {
	f.once.Do(func() {
		f.ch <- r
		close(f.ch)
	})
}

// Done returns a channel that yields the single Result then closes.
func (f *Future) Done() <-chan Result { return f.ch }

// Await blocks until the request resolves.
func (f *Future) Await() Result { return <-f.ch }

// Stats counts scheduler events. Read with Scheduler.Stats.
type Stats struct {
	Sent          int
	Completed     int
	Timeouts      int
	Cancelled     int
	LateResponses int
}

// Scheduler keeps at most one request in flight, retries dispatch as
// transaction numbers leave isolation, and matches responses to the
// in-flight request by transaction number.
type Scheduler struct {
	cfg       Config
	clock     Clock
	transport Transport

	events chan interface{}
	done   chan struct{}
	closed int32 // atomic; fast-path reject in Submit

	mu    sync.Mutex
	stats Stats
}

type request struct {
	id        uint64
	payload   []byte
	fut       *Future
	ctx       context.Context
	stopWatch chan struct{}
	finished  bool
}

type inflightRequest struct {
	req   *request
	txn   int
	gen   uint64
	timer Timer
}

type submitEvent struct{ req *request }
type responseEvent struct {
	txn     int
	payload []byte
}
type timeoutEvent struct {
	gen uint64
	txn int
}
type cancelEvent struct {
	id  uint64
	err error
}
type txnFreeEvent struct{}
type closeEvent struct{}
type pingEvent struct{ ack chan struct{} }

// NewScheduler starts a scheduler loop. Call Close to stop it.
func NewScheduler(cfg Config, clock Clock, transport Transport) *Scheduler {
	if clock == nil {
		clock = RealClock{}
	}
	s := &Scheduler{
		cfg:       cfg.withDefaults(),
		clock:     clock,
		transport: transport,
		events:    make(chan interface{}),
		done:      make(chan struct{}),
	}
	go s.run()
	return s
}

// Submit enqueues a request. Cancelling ctx while the request is queued
// removes it; cancelling while it is in flight unblocks the caller with
// the context error but the device operation runs to completion, so the
// in-flight exchange is never disturbed.
func (s *Scheduler) Submit(ctx context.Context, payload []byte) (*Future, error) {
	if atomic.LoadInt32(&s.closed) != 0 {
		return nil, ErrClosed
	}
	req := &request{
		payload:   payload,
		fut:       newFuture(),
		ctx:       ctx,
		stopWatch: make(chan struct{}),
	}
	ev := submitEvent{req: req}
	select {
	case s.events <- ev:
		return req.fut, nil
	case <-s.done:
		return nil, ErrClosed
	}
}

// HandleResponse feeds a device response into the scheduler. A response
// whose transaction number does not match the in-flight request is a
// late (or duplicate) answer and is dropped; it never completes a newer
// request.
func (s *Scheduler) HandleResponse(txn int, payload []byte) {
	s.sendEvent(responseEvent{txn: txn, payload: payload})
}

// Close stops accepting submissions, fails queued requests with
// ErrShutdown, and lets the in-flight exchange finish (response or
// timeout) before the loop exits. It is idempotent.
func (s *Scheduler) Close() {
	if atomic.CompareAndSwapInt32(&s.closed, 0, 1) {
		s.sendEvent(closeEvent{})
	}
}

// Wait blocks until the scheduler loop has fully drained and exited.
func (s *Scheduler) Wait() { <-s.done }

// Stats returns a snapshot of the event counters.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Scheduler) sendEvent(ev interface{}) {
	select {
	case s.events <- ev:
	case <-s.done:
	}
}

// sync is used by tests: it returns once every event sent before it has
// been processed by the loop.
func (s *Scheduler) sync() {
	ack := make(chan struct{})
	s.sendEvent(pingEvent{ack: ack})
	<-ack
}

func (s *Scheduler) bump(f func(*Stats)) {
	s.mu.Lock()
	f(&s.stats)
	s.mu.Unlock()
}

func (s *Scheduler) run() {
	var (
		queue    []*request
		inflight *inflightRequest
		alloc    = newTxnAllocator(s.cfg.TxnSpaceSize, s.cfg.Isolation)
		nextID   uint64
		nextGen  uint64
		closed   bool
	)

	finishRequest := func(req *request, res Result) {
		if req.finished {
			return
		}
		req.finished = true
		req.fut.complete(res)
		close(req.stopWatch)
	}

	dispatch := func() {
		for !closed && inflight == nil && len(queue) > 0 {
			now := s.clock.Now()
			txn, ok := alloc.acquire(now)
			if !ok {
				// Every number is isolated; wake up when the
				// earliest one leaves quarantine.
				at := alloc.earliestFree()
				s.clock.AfterFunc(at.Sub(now), func() {
					s.sendEvent(txnFreeEvent{})
				})
				return
			}
			req := queue[0]
			queue = queue[1:]
			gen := nextGen
			nextGen++
			timer := s.clock.AfterFunc(s.cfg.Timeout, func() {
				s.sendEvent(timeoutEvent{gen: gen, txn: txn})
			})
			if err := s.transport.Send(txn, req.payload); err != nil {
				timer.Stop()
				alloc.release(txn, now)
				finishRequest(req, Result{Err: err})
				continue
			}
			s.bump(func(st *Stats) { st.Sent++ })
			inflight = &inflightRequest{req: req, txn: txn, gen: gen, timer: timer}
		}
	}

	dequeue := func(id uint64) *request {
		for i, req := range queue {
			if req.id == id {
				queue = append(queue[:i], queue[i+1:]...)
				return req
			}
		}
		return nil
	}

	for {
		select {
		case ev := <-s.events:
			switch e := ev.(type) {
			case submitEvent:
				if closed {
					finishRequest(e.req, Result{Err: ErrClosed})
					break
				}
				if s.cfg.QueueSize > 0 && len(queue) >= s.cfg.QueueSize {
					finishRequest(e.req, Result{Err: ErrQueueFull})
					break
				}
				e.req.id = nextID
				nextID++
				req := e.req
				queue = append(queue, req)
				go func() {
					select {
					case <-req.ctx.Done():
						s.sendEvent(cancelEvent{id: req.id, err: req.ctx.Err()})
					case <-req.stopWatch:
					}
				}()
				dispatch()
			case responseEvent:
				if inflight != nil && inflight.txn == e.txn {
					inflight.timer.Stop()
					alloc.release(inflight.txn, s.clock.Now())
					req := inflight.req
					inflight = nil
					s.bump(func(st *Stats) { st.Completed++ })
					finishRequest(req, Result{Resp: Response{Txn: e.txn, Payload: e.payload}})
					dispatch()
				} else {
					// Late or duplicate answer: the exchange it
					// belongs to is already over. Drop it so it
					// can never complete a newer request.
					s.bump(func(st *Stats) { st.LateResponses++ })
				}
			case timeoutEvent:
				if inflight != nil && inflight.gen == e.gen {
					alloc.release(inflight.txn, s.clock.Now())
					req := inflight.req
					inflight = nil
					s.bump(func(st *Stats) { st.Timeouts++ })
					finishRequest(req, Result{Err: ErrTimeout})
					dispatch()
				}
			case cancelEvent:
				if req := dequeue(e.id); req != nil {
					s.bump(func(st *Stats) { st.Cancelled++ })
					finishRequest(req, Result{Err: e.err})
					dispatch()
				} else if inflight != nil && inflight.req.id == e.id {
					// Unblock the caller, but keep the device
					// exchange running until response/timeout.
					finishRequest(inflight.req, Result{Err: e.err})
				}
			case txnFreeEvent:
				dispatch()
			case closeEvent:
				if closed {
					break
				}
				closed = true
				for _, req := range queue {
					finishRequest(req, Result{Err: ErrShutdown})
				}
				queue = nil
			case pingEvent:
				close(e.ack)
			}
		}
		if closed && inflight == nil && len(queue) == 0 {
			close(s.done)
			return
		}
	}
}

// txnAllocator hands out transaction numbers round-robin, skipping any
// number still inside its isolation window.
type txnAllocator struct {
	size          int
	isolation     time.Duration
	next          int
	isolatedUntil []time.Time
}

func newTxnAllocator(size int, isolation time.Duration) *txnAllocator {
	return &txnAllocator{
		size:          size,
		isolation:     isolation,
		isolatedUntil: make([]time.Time, size),
	}
}

func (a *txnAllocator) acquire(now time.Time) (int, bool) {
	for i := 0; i < a.size; i++ {
		txn := a.next
		a.next = (a.next + 1) % a.size
		if !now.Before(a.isolatedUntil[txn]) {
			return txn, true
		}
	}
	return 0, false
}

func (a *txnAllocator) release(txn int, now time.Time) {
	a.isolatedUntil[txn] = now.Add(a.isolation)
}

func (a *txnAllocator) earliestFree() time.Time {
	var min time.Time
	for _, t := range a.isolatedUntil {
		if min.IsZero() || t.Before(min) {
			min = t
		}
	}
	return min
}
