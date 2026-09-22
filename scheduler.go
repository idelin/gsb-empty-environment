// Package keyq provides a keyed task scheduler: tasks submitted with the
// same key run strictly one-at-a-time in submission order, while tasks with
// different keys run in parallel across a dynamically resizable set of
// shards. Only the Go standard library is used.
package keyq

import (
	"context"
	"errors"
	"hash/fnv"
	"sync/atomic"
)

var (
	// ErrSchedulerClosed is delivered to handles submitted after Shutdown.
	ErrSchedulerClosed = errors.New("keyq: scheduler is shut down")
	// ErrTaskCancelled is delivered to a handle whose queued task was
	// cancelled before it started.
	ErrTaskCancelled = errors.New("keyq: task cancelled")
)

// TaskFunc is the unit of work. The ctx is cancelled if the task is
// cancelled while running; tasks should honor it cooperatively.
type TaskFunc func(ctx context.Context) error

// Handle tracks one submitted task.
type Handle struct {
	key    string
	sched  *Scheduler
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	err    error // written by the dispatcher before done is closed
}

// Wait blocks until the task finishes, is cancelled, or is rejected,
// and returns its outcome.
func (h *Handle) Wait() error {
	<-h.done
	return h.err
}

// Done returns a channel closed when the task settles.
func (h *Handle) Done() <-chan struct{} { return h.done }

// Cancel removes the task if it is still queued, returning true. If the
// task is already running, its context is cancelled instead and Cancel
// returns false. Cancelling one queued task never reorders the rest.
func (h *Handle) Cancel() bool {
	select {
	case <-h.done:
		return false
	default:
	}
	req := cancelReq{h: h, ack: make(chan bool, 1)}
	select {
	case h.sched.cancelCh <- req:
		return <-req.ack
	case <-h.done:
		return false
	case <-h.sched.loopDone:
		return false
	}
}

type config struct {
	shards int
	hash   func(string) uint32
}

// Option customizes a Scheduler.
type Option func(*config)

// WithShards sets the initial shard count (default 4).
func WithShards(n int) Option {
	return func(c *config) {
		if n >= 1 {
			c.shards = n
		}
	}
}

// WithHashFunc overrides the key->shard hash. Mainly useful for tests
// that need deterministic key placement.
func WithHashFunc(f func(string) uint32) Option {
	return func(c *config) { c.hash = f }
}

func defaultHash(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

// Scheduler dispatches keyed tasks to shard workers. All mutable
// scheduling state lives in a single dispatcher goroutine, so per-key
// FIFO queues never move during rescales and no locks are needed on the
// hot path.
type Scheduler struct {
	hash func(string) uint32

	submitCh   chan submitReq
	cancelCh   chan cancelReq
	rescaleCh  chan rescaleReq
	doneCh     chan shardDone
	exitedCh   chan *shard
	shutdownCh chan shutdownReq
	loopDone   chan struct{}

	shardCount atomic.Int32
}

// New creates a Scheduler and starts its shard workers.
func New(opts ...Option) *Scheduler {
	cfg := config{shards: 4, hash: defaultHash}
	for _, o := range opts {
		o(&cfg)
	}
	s := &Scheduler{
		hash:       cfg.hash,
		submitCh:   make(chan submitReq),
		cancelCh:   make(chan cancelReq),
		rescaleCh:  make(chan rescaleReq),
		doneCh:     make(chan shardDone),
		exitedCh:   make(chan *shard),
		shutdownCh: make(chan shutdownReq),
		loopDone:   make(chan struct{}),
	}
	s.shardCount.Store(int32(cfg.shards))
	go s.loop(cfg.shards)
	return s
}

// Submit enqueues fn under key. Same-key tasks execute strictly in
// submission order, one at a time.
func (s *Scheduler) Submit(key string, fn TaskFunc) *Handle {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handle{key: key, sched: s, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	select {
	case s.submitCh <- submitReq{key: key, fn: fn, h: h}:
	case <-s.loopDone:
		h.err = ErrSchedulerClosed
		close(h.done)
	}
	return h
}

// Rescale changes the number of shard workers and returns once the new
// shard set is installed. In-flight tasks finish on their old shard
// (safe handoff); queued tasks keep their per-key order and are routed
// with the new shard count.
func (s *Scheduler) Rescale(n int) {
	ack := make(chan struct{})
	select {
	case s.rescaleCh <- rescaleReq{n: n, ack: ack}:
		<-ack
	case <-s.loopDone:
	}
}

// ShardCount reports the current number of shard workers.
func (s *Scheduler) ShardCount() int { return int(s.shardCount.Load()) }

// Shutdown stops accepting new submissions, drains all queued and
// in-flight tasks, stops every worker, and then returns.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	ack := make(chan error, 1)
	select {
	case s.shutdownCh <- shutdownReq{ack: ack}:
	case <-s.loopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-ack:
		return err
	case <-s.loopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- dispatcher internals (all state below is owned by loop) ---

type queuedTask struct {
	key string
	fn  TaskFunc
	h   *Handle
}

type keyState struct {
	queue   []queuedTask
	running *Handle // non-nil while a task of this key is in flight
}

type shard struct {
	idx      int // position within the current generation, used by hash%len
	id       int // globally unique, for debugging
	ch       chan queuedTask
	stopCh   chan struct{}
	busy     bool
	stopping bool
}

type submitReq struct {
	key string
	fn  TaskFunc
	h   *Handle
}

type cancelReq struct {
	h   *Handle
	ack chan bool
}

type rescaleReq struct {
	n   int
	ack chan struct{}
}

type shutdownReq struct {
	ack chan error
}

type shardDone struct {
	sh  *shard
	key string
	h   *Handle
	err error
}

// run is the shard worker. On stop it first drains a possibly buffered
// task so a rescale racing a dispatch never loses work.
func (sh *shard) run(doneCh chan<- shardDone, exitedCh chan<- *shard) {
	for {
		select {
		case t := <-sh.ch:
			doneCh <- shardDone{sh: sh, key: t.key, h: t.h, err: t.fn(t.h.ctx)}
		case <-sh.stopCh:
			select {
			case t := <-sh.ch:
				doneCh <- shardDone{sh: sh, key: t.key, h: t.h, err: t.fn(t.h.ctx)}
			default:
			}
			exitedCh <- sh
			return
		}
	}
}

func (s *Scheduler) loop(n int) {
	defer close(s.loopDone)

	keys := make(map[string]*keyState)
	var shards []*shard
	var stopping []*shard
	closing := false
	var shutdownAcks []chan error
	nextID := 0

	rescale := func(count int) {
		if count < 1 {
			count = 1
		}
		for _, sh := range shards {
			if !sh.stopping {
				sh.stopping = true
				close(sh.stopCh)
				stopping = append(stopping, sh)
			}
		}
		shards = make([]*shard, 0, count)
		for i := 0; i < count; i++ {
			sh := &shard{idx: i, id: nextID, ch: make(chan queuedTask, 1), stopCh: make(chan struct{})}
			nextID++
			shards = append(shards, sh)
			go sh.run(s.doneCh, s.exitedCh)
		}
		s.shardCount.Store(int32(count))
	}

	// schedule dispatches ready keys (queued, nothing in flight) to idle
	// shards of the current generation.
	schedule := func() {
		for _, sh := range shards {
			if sh.busy || sh.stopping {
				continue
			}
			for key, ks := range keys {
				if len(ks.queue) == 0 || ks.running != nil {
					continue
				}
				if int(s.hash(key)%uint32(len(shards))) != sh.idx {
					continue
				}
				t := ks.queue[0]
				ks.queue = ks.queue[1:]
				ks.running = t.h
				sh.busy = true
				sh.ch <- t
				break
			}
		}
	}

	// maybeFinish reports whether the loop should exit. When draining is
	// complete it stops the current shards; once every worker has exited
	// it acknowledges Shutdown callers.
	maybeFinish := func() bool {
		if !closing || len(keys) != 0 {
			return false
		}
		for _, sh := range shards {
			if !sh.stopping {
				sh.stopping = true
				close(sh.stopCh)
				stopping = append(stopping, sh)
			}
		}
		shards = nil
		if len(stopping) != 0 {
			return false
		}
		for _, ack := range shutdownAcks {
			ack <- nil
		}
		return true
	}

	rescale(n)

	for {
		select {
		case req := <-s.submitCh:
			if closing {
				req.h.err = ErrSchedulerClosed
				close(req.h.done)
				continue
			}
			ks := keys[req.key]
			if ks == nil {
				ks = &keyState{}
				keys[req.key] = ks
			}
			ks.queue = append(ks.queue, queuedTask{key: req.key, fn: req.fn, h: req.h})
			schedule()

		case req := <-s.cancelCh:
			h := req.h
			removed := false
			if ks := keys[h.key]; ks != nil {
				for i, t := range ks.queue {
					if t.h == h {
						ks.queue = append(ks.queue[:i], ks.queue[i+1:]...)
						h.err = ErrTaskCancelled
						close(h.done)
						removed = true
						if len(ks.queue) == 0 && ks.running == nil {
							delete(keys, h.key) // reclaim empty key state
						}
						break
					}
				}
				if !removed && ks.running == h {
					h.cancel() // cooperative cancellation of the running task
				}
			}
			req.ack <- removed
			if maybeFinish() {
				return
			}

		case req := <-s.rescaleCh:
			if !closing {
				rescale(req.n)
				schedule()
			}
			close(req.ack)

		case d := <-s.doneCh:
			if ks := keys[d.key]; ks != nil {
				ks.running = nil
				if len(ks.queue) == 0 {
					delete(keys, d.key) // reclaim empty key state
				}
			}
			d.sh.busy = false
			d.h.err = d.err
			close(d.h.done)
			schedule()
			if maybeFinish() {
				return
			}

		case sh := <-s.exitedCh:
			for i, x := range stopping {
				if x == sh {
					stopping = append(stopping[:i], stopping[i+1:]...)
					break
				}
			}
			if maybeFinish() {
				return
			}

		case req := <-s.shutdownCh:
			closing = true
			shutdownAcks = append(shutdownAcks, req.ack)
			if maybeFinish() {
				return
			}
		}
	}
}
