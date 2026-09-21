// Package scheduler implements a key-ordered task scheduler with dynamic
// sharding. Tasks with the same key are executed strictly one at a time in
// submission order; tasks with different keys run in parallel. The number of
// shards can be grown or shrunk at runtime without violating per-key
// ordering: a key's next task is only ever dispatched after its previous
// task has finished, so migrating a key to a new shard is always a safe
// handoff through the completion path.
//
// Only the Go standard library is used (sync, sync/atomic, context, hash/fnv).
package scheduler

import (
	"context"
	"errors"
	"hash/fnv"
	"sync"
)

var (
	// ErrClosed is returned by Submit after the scheduler has been closed.
	ErrClosed = errors.New("scheduler: closed")
	// ErrCanceled is delivered on the Future of a canceled task.
	ErrCanceled = errors.New("scheduler: task canceled")
)

// TaskFunc is the unit of work injected by the caller (in tests: timing,
// failures and side effects are all controlled through this function).
type TaskFunc func(ctx context.Context) error

// Future is the handle for a submitted task. Exactly one value is ever
// delivered on the channel returned by Done.
type Future struct {
	done chan error
	sch  *Scheduler
	qt   *queuedTask
}

// Done returns a channel that receives the task's result exactly once:
// nil on success, the TaskFunc's error, or ErrCanceled.
func (f *Future) Done() <-chan error { return f.done }

// Wait blocks until the task finishes and returns its result.
func (f *Future) Wait() error { return <-f.done }

// Cancel requests cancellation. Pending tasks are removed from their key
// queue (remaining tasks keep their relative order); a running task gets
// its context canceled and its result replaced with ErrCanceled. Returns
// false if the task had already finished.
func (f *Future) Cancel() bool { return f.sch.cancel(f.qt) }

type queuedTask struct {
	key      string
	fn       TaskFunc
	fut      *Future
	ctx      context.Context
	cancelFn context.CancelFunc
	// The fields below are guarded by Scheduler.mu.
	canceled bool
	running  bool // popped from the key queue, dispatched to a shard
	resolved bool
}

func (qt *queuedTask) resolveLocked(err error) {
	if qt.resolved {
		return
	}
	qt.resolved = true
	qt.cancelFn() // release the context; fn has returned or will observe cancel
	qt.fut.done <- err
}

// keyQueue is the per-key FIFO. Invariant: if tasks is non-empty or a task
// is dispatched/running, active is true and exactly one unit of this key is
// in flight; its completion dispatches the next queued task.
type keyQueue struct {
	tasks  []*queuedTask // pending, not yet dispatched
	active bool          // a task is dispatched or running
}

type unit struct {
	key string
	qt  *queuedTask
}

// shard is an unbounded FIFO consumed by a fixed set of worker goroutines.
// push never blocks, so the scheduler mutex is never held while blocking on
// a shard, which keeps dispatch and completion deadlock-free.
type shard struct {
	mu     sync.Mutex
	cond   sync.Cond
	queue  []unit
	closed bool
}

func newShard() *shard {
	sh := &shard{}
	sh.cond.L = &sh.mu
	return sh
}

func (sh *shard) push(u unit) {
	sh.mu.Lock()
	sh.queue = append(sh.queue, u)
	sh.mu.Unlock()
	sh.cond.Signal()
}

// close stops the shard: workers drain the remaining queue and then exit.
func (sh *shard) close() {
	sh.mu.Lock()
	sh.closed = true
	sh.mu.Unlock()
	sh.cond.Broadcast()
}

// waitDrained blocks until every queued unit has been picked up by a
// worker. Used by Reshard to confirm the old shard has safely handed off.
func (sh *shard) waitDrained() {
	sh.mu.Lock()
	for len(sh.queue) > 0 {
		sh.cond.Wait()
	}
	sh.mu.Unlock()
}

// Scheduler dispatches key-ordered tasks onto a dynamically sized set of
// shards. The zero value is not usable; construct with New.
type Scheduler struct {
	mu              sync.Mutex
	shards          []*shard // current generation; dispatch always uses this
	keys            map[string]*keyQueue
	closed          bool
	epoch           uint64 // bumped on every Reshard
	workersPerShard int

	inflight  sync.WaitGroup // dispatched but unfinished tasks
	workersWg sync.WaitGroup // every worker goroutine ever spawned
}

// Option configures a Scheduler.
type Option func(*Scheduler)

// WithShards sets the initial number of shards (default 4).
func WithShards(n int) Option {
	return func(s *Scheduler) {
		if n > 0 {
			s.shards = make([]*shard, n)
		}
	}
}

// WithWorkersPerShard sets how many worker goroutines each shard runs
// (default 4), bounding how many distinct keys of one shard execute
// concurrently.
func WithWorkersPerShard(n int) Option {
	return func(s *Scheduler) {
		if n > 0 {
			s.workersPerShard = n
		}
	}
}

// New creates a Scheduler and starts its shard workers.
func New(opts ...Option) *Scheduler {
	s := &Scheduler{
		shards:          make([]*shard, 4),
		keys:            make(map[string]*keyQueue),
		workersPerShard: 4,
	}
	for _, opt := range opts {
		opt(s)
	}
	if len(s.shards) == 0 {
		s.shards = make([]*shard, 1)
	}
	for i := range s.shards {
		s.shards[i] = newShard()
		s.startWorkersLocked(s.shards[i])
	}
	return s
}

func (s *Scheduler) startWorkersLocked(sh *shard) {
	for i := 0; i < s.workersPerShard; i++ {
		s.workersWg.Add(1)
		go s.workerLoop(sh)
	}
}

func (s *Scheduler) workerLoop(sh *shard) {
	defer s.workersWg.Done()
	for {
		sh.mu.Lock()
		for len(sh.queue) == 0 && !sh.closed {
			sh.cond.Wait()
		}
		if len(sh.queue) == 0 { // closed and drained
			sh.mu.Unlock()
			return
		}
		u := sh.queue[0]
		copy(sh.queue, sh.queue[1:])
		sh.queue[len(sh.queue)-1] = unit{}
		sh.queue = sh.queue[:len(sh.queue)-1]
		sh.mu.Unlock()
		sh.cond.Signal() // a slot was consumed; wake waitDrained
		s.runUnit(u)
	}
}

func (s *Scheduler) runUnit(u unit) {
	s.mu.Lock()
	canceled := u.qt.canceled
	s.mu.Unlock()
	if canceled {
		s.finish(u.key, u.qt, ErrCanceled)
		return
	}
	err := u.qt.fn(u.qt.ctx)
	s.finish(u.key, u.qt, err)
}

// finish resolves qt and hands the key off to its next queued task, which
// is dispatched onto the *current* shard generation. Because a key has at
// most one task in flight, this completion-chained dispatch is what keeps
// per-key order intact across resharding.
func (s *Scheduler) finish(key string, qt *queuedTask, runErr error) {
	s.mu.Lock()
	err := runErr
	if qt.canceled {
		err = ErrCanceled
	}
	qt.resolveLocked(err)

	kq := s.keys[key]
	for len(kq.tasks) > 0 {
		next := kq.tasks[0]
		kq.tasks[0] = nil
		kq.tasks = kq.tasks[1:]
		if next.canceled {
			next.resolveLocked(ErrCanceled)
			continue
		}
		s.dispatchLocked(key, next)
		s.mu.Unlock()
		s.inflight.Done()
		return
	}
	// Key queue is empty and nothing is in flight: reclaim the entry.
	// This happens under the same mutex as Submit, so a concurrent
	// enqueue either sees the live entry or creates a fresh one — a task
	// can never be appended to an entry that is being deleted.
	kq.active = false
	delete(s.keys, key)
	s.mu.Unlock()
	s.inflight.Done()
}

// dispatchLocked pops nothing; qt must already be dequeued. It assigns qt
// to a shard of the current generation. Caller holds s.mu.
func (s *Scheduler) dispatchLocked(key string, qt *queuedTask) {
	qt.running = true
	sh := s.shards[hashKey(key)%uint32(len(s.shards))]
	s.inflight.Add(1)
	sh.push(unit{key: key, qt: qt})
}

// Submit enqueues fn for key. Tasks of the same key run strictly one at a
// time in submission order; tasks of different keys may run in parallel.
func (s *Scheduler) Submit(key string, fn TaskFunc) (*Future, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	qt := &queuedTask{key: key, fn: fn}
	qt.ctx, qt.cancelFn = context.WithCancel(context.Background())
	f := &Future{done: make(chan error, 1), sch: s, qt: qt}
	qt.fut = f

	kq := s.keys[key]
	if kq == nil {
		kq = &keyQueue{}
		s.keys[key] = kq
	}
	if kq.active {
		kq.tasks = append(kq.tasks, qt)
	} else {
		kq.active = true
		s.dispatchLocked(key, qt)
	}
	s.mu.Unlock()
	return f, nil
}

func (s *Scheduler) cancel(qt *queuedTask) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if qt.resolved {
		return false
	}
	qt.canceled = true
	qt.cancelFn()
	if !qt.running {
		// Still pending: unlink from the key queue. Remaining tasks keep
		// their relative order.
		if kq, ok := s.keys[qt.key]; ok {
			for i, t := range kq.tasks {
				if t == qt {
					copy(kq.tasks[i:], kq.tasks[i+1:])
					kq.tasks[len(kq.tasks)-1] = nil
					kq.tasks = kq.tasks[:len(kq.tasks)-1]
					break
				}
			}
		}
		qt.resolveLocked(ErrCanceled)
	}
	// Running tasks are resolved with ErrCanceled in finish.
	return true
}

// Reshard migrates the scheduler to n shards. In-flight tasks finish on
// their old workers; every subsequent dispatch uses the new generation, and
// because a key's successor is only dispatched from its predecessor's
// completion path, per-key order is preserved across the migration. Reshard
// blocks until the previous shard generation has handed off (drained) all
// queued-but-unstarted work.
func (s *Scheduler) Reshard(n int) {
	if n < 1 {
		n = 1
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	old := s.shards
	if len(old) == n {
		s.mu.Unlock()
		return
	}
	fresh := make([]*shard, n)
	for i := range fresh {
		fresh[i] = newShard()
		s.startWorkersLocked(fresh[i])
	}
	s.shards = fresh
	s.epoch++
	s.mu.Unlock()

	for _, sh := range old {
		sh.close()
	}
	for _, sh := range old {
		sh.waitDrained()
	}
}

// Epoch returns the current shard generation (incremented by Reshard).
func (s *Scheduler) Epoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

// ShardCount returns the current number of shards.
func (s *Scheduler) ShardCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.shards)
}

// ShardIndex reports which shard of the current generation key maps to.
func (s *Scheduler) ShardIndex(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(hashKey(key) % uint32(len(s.shards)))
}

// KeyCount returns the number of keys with live queues (mainly for tests
// and introspection of empty-queue reclamation).
func (s *Scheduler) KeyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}

// Close stops accepting submissions, waits for all queued and running
// tasks to finish, then shuts down every shard worker.
func (s *Scheduler) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.workersWg.Wait()
		return
	}
	s.closed = true
	shards := s.shards
	s.mu.Unlock()

	// Every queued task has an in-flight predecessor (or is itself in
	// flight), so when inflight reaches zero all key queues are empty.
	s.inflight.Wait()
	for _, sh := range shards {
		sh.close()
	}
	s.workersWg.Wait()
}

func hashKey(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}
