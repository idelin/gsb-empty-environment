package prefetch

import (
	"container/heap"
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned by Get after the scheduler is closed.
var ErrClosed = errors.New("prefetch: scheduler closed")

// Source loads a value for a key. It is an injectable function: tests and
// demos supply fakes, failures included. No remote cache is required.
type Source interface {
	Load(ctx context.Context, key string) (Value, error)
}

// SourceFunc adapts a plain function to Source.
type SourceFunc func(ctx context.Context, key string) (Value, error)

func (f SourceFunc) Load(ctx context.Context, key string) (Value, error) { return f(ctx, key) }

type result struct {
	val Value
	err error
}

type taskState int

const (
	taskPending taskState = iota
	taskRunning
)

// task is the identity of one in-flight or queued load for a key. A real
// request promoting a prefetch keeps this exact task: no duplicate load.
type task struct {
	id      uint64
	key     string
	gen     uint64
	seq     uint64
	state   taskState
	heapIdx int
	waiters []chan result
	pfRefs  int
	fgClass bool // running in a foreground-accounted slot
	cancel  context.CancelFunc
}

func (t *task) foreground() bool { return len(t.waiters) > 0 }

// Scheduler coalesces loads of concurrent loads per key, schedules prefetch
// work at low priority under a shared budget, and reserves foreground
// slots so prefetch can never starve real requests.
type Scheduler struct {
	src       Source
	cache     *Cache
	maxTotal  int
	fgReserve int

	mu     sync.Mutex
	gen    uint64
	tasks  map[string]*task
	queue  taskHeap
	fgRun  int
	pfRun  int
	seq    uint64
	nextID uint64
	closed bool
	wg     sync.WaitGroup
}

// NewScheduler builds a scheduler running at most maxTotal concurrent
// loads, of which at most maxTotal-fgReserve may be prefetch. fgReserve
// must be in [0, maxTotal).
func NewScheduler(src Source, cache *Cache, maxTotal, fgReserve int) *Scheduler {
	if maxTotal < 1 || fgReserve < 0 || fgReserve >= maxTotal {
		panic("prefetch: require 0 <= fgReserve < maxTotal")
	}
	return &Scheduler{
		src:       src,
		cache:     cache,
		maxTotal:  maxTotal,
		fgReserve: fgReserve,
		tasks:     make(map[string]*task),
	}
}

// Stats is a point-in-time view for tests and demos.
type Stats struct {
	Generation uint64
	Tasks      int
	Pending    int
	FGRunning  int
	PFRunning  int
}

func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Generation: s.gen,
		Tasks:      len(s.tasks),
		Pending:    s.queue.Len(),
		FGRunning:  s.fgRun,
		PFRunning:  s.pfRun,
	}
}

// Get returns the value for key, serving from cache when possible and
// otherwise joining (or promoting) the single load task for that key.
func (s *Scheduler) Get(ctx context.Context, key string) (Value, error) {
	if v, ok := s.cache.Get(key); ok {
		return v, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Value{}, ErrClosed
	}
	t := s.tasks[key]
	if t == nil {
		t = &task{id: s.nextID, key: key, gen: s.gen, seq: s.seq, heapIdx: -1}
		s.nextID++
		s.seq++
		s.tasks[key] = t
		heap.Push(&s.queue, t)
	}
	hadWaiters := len(t.waiters) > 0
	ch := make(chan result, 1)
	t.waiters = append(t.waiters, ch)
	if !hadWaiters {
		switch t.state {
		case taskPending:
			heap.Fix(&s.queue, t.heapIdx)
		case taskRunning:
			if !t.fgClass {
				s.pfRun--
				s.fgRun++
				t.fgClass = true
			}
		}
	}
	s.pumpLocked()
	s.mu.Unlock()

	select {
	case r := <-ch:
		return r.val, r.err
	case <-ctx.Done():
		s.removeWaiter(t, ch)
		return Value{}, ctx.Err()
	}
}

// Prefetch subscribes speculative interest for keys at generation gen.
// Calls from stale generations are ignored.
func (s *Scheduler) Prefetch(gen uint64, keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || gen < s.gen {
		return
	}
	for _, key := range keys {
		if _, ok := s.cache.Get(key); ok {
			continue
		}
		t := s.tasks[key]
		if t == nil {
			t = &task{id: s.nextID, key: key, gen: gen, seq: s.seq, heapIdx: -1}
			s.nextID++
			s.seq++
			s.tasks[key] = t
			heap.Push(&s.queue, t)
		}
		t.pfRefs++
		if t.gen < gen {
			t.gen = gen
		}
	}
	s.pumpLocked()
}

// AdvanceGeneration starts a new prediction generation. Prefetch
// subscriptions from older generations are removed; tasks with no real
// waiters left are canceled (pending ones dequeued, running ones get
// their context canceled and their result dropped). Real waiters are
// never affected. Returns the new generation.
func (s *Scheduler) AdvanceGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	for _, t := range s.tasks {
		if t.pfRefs > 0 && t.gen < s.gen {
			t.pfRefs = 0
			if len(t.waiters) == 0 {
				s.cancelTaskLocked(t)
			}
		}
	}
	s.pumpLocked()
	return s.gen
}

// Close cancels everything and waits for in-flight loads to unwind.
func (s *Scheduler) Close() {
	s.mu.Lock()
	s.closed = true
	for _, t := range s.tasks {
		s.cancelTaskLocked(t)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Scheduler) removeWaiter(t *task, ch chan result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, w := range t.waiters {
		if w == ch {
			t.waiters = append(t.waiters[:i], t.waiters[i+1:]...)
			break
		}
	}
	if s.tasks[t.key] != t {
		return
	}
	if len(t.waiters) == 0 && t.pfRefs == 0 {
		s.cancelTaskLocked(t)
	}
	s.pumpLocked()
}

// cancelTaskLocked detaches t from the task table. Pending tasks are
// dequeued; running tasks keep their slot accounting until the goroutine
// exits, but their context is canceled and their result will be dropped.
func (s *Scheduler) cancelTaskLocked(t *task) {
	if s.tasks[t.key] != t {
		return
	}
	delete(s.tasks, t.key)
	if t.state == taskPending {
		heap.Remove(&s.queue, t.heapIdx)
	} else if t.cancel != nil {
		t.cancel()
	}
}

// pumpLocked starts queued tasks while class budgets allow. Prefetch may
// only use maxTotal-fgReserve slots; foreground may use all of them.
func (s *Scheduler) pumpLocked() {
	for s.queue.Len() > 0 {
		t := s.queue[0]
		if t.foreground() {
			if s.fgRun+s.pfRun >= s.maxTotal {
				return
			}
			s.fgRun++
			t.fgClass = true
		} else {
			if s.fgRun+s.pfRun >= s.maxTotal-s.fgReserve {
				return
			}
			s.pfRun++
		}
		heap.Pop(&s.queue)
		t.state = taskRunning
		t.heapIdx = -1
		ctx, cancel := context.WithCancel(context.Background())
		t.cancel = cancel
		s.wg.Add(1)
		go s.run(t, ctx)
	}
}

func (s *Scheduler) run(t *task, ctx context.Context) {
	v, err := s.src.Load(ctx, t.key)
	s.mu.Lock()
	if t.fgClass {
		s.fgRun--
	} else {
		s.pfRun--
	}
	var waiters []chan result
	if s.tasks[t.key] == t {
		delete(s.tasks, t.key)
		waiters = t.waiters
		if err == nil {
			switch {
			case len(t.waiters) > 0:
				s.cache.Publish(t.key, v, false)
			case t.pfRefs > 0:
				s.cache.Publish(t.key, v, true)
			}
		}
	}
	s.pumpLocked()
	s.mu.Unlock()
	for _, ch := range waiters {
		ch <- result{val: v, err: err}
	}
	s.wg.Done()
}

// taskHeap orders foreground tasks before prefetch, FIFO within a class.
type taskHeap []*task

func (h taskHeap) Len() int { return len(h) }

func (h taskHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	fa, fb := a.foreground(), b.foreground()
	if fa != fb {
		return fa
	}
	return a.seq < b.seq
}

func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}

func (h *taskHeap) Push(x any) {
	t := x.(*task)
	t.heapIdx = len(*h)
	*h = append(*h, t)
}

func (h *taskHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	t.heapIdx = -1
	*h = old[:n-1]
	return t
}
