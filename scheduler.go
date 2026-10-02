package memsched

import (
	"sync"
	"time"
)

// Task is a unit of batch work submitted to the scheduler.
type Task struct {
	Name string
	// GCCapable marks tasks whose fiber can reclaim memory (via Fiber.GC).
	// Under memory pressure the scheduler admits only GC-capable tasks.
	GCCapable bool
	// MemDemand is the estimated temporary memory the task will allocate.
	// It is reserved in the Monitor at admission time.
	MemDemand int64
	// Run is the task body. It receives the fiber for memory accounting.
	Run func(f *Fiber)
}

// Scheduler admits fibers subject to memory backpressure:
//   - usage >= high watermark: admission of new (especially memory-hungry)
//     fibers is suspended; GC-capable fibers are scheduled with priority;
//   - usage drops below low watermark: admission resumes smoothly through a
//     token bucket that refills over time, instead of all at once.
type Scheduler struct {
	mon  *Monitor
	mu   sync.Mutex
	cond *sync.Cond // for Wait/Close waiters only
	wake chan struct{}

	queue   []*Task
	running int
	closed  bool

	pressured  bool // latched between high and low watermarks
	recovering bool // smooth-recovery ramp in progress

	// Token bucket pacing admissions while recovering.
	pacingInterval time.Duration
	tokens         float64
	maxTokens      float64
	lastRefill     time.Time
	pacingTimer    *time.Timer

	// Statistics (guarded by mu).
	Admitted         int64 // fibers admitted so far
	Completed        int64 // fibers finished so far
	BlockedEvents    int64 // times admission was deferred by backpressure
	PressureEpisodes int64 // times usage crossed the high watermark
	Violations       int64 // non-GC admissions while pressured (must stay 0)
}

const (
	defaultPacingInterval = 10 * time.Millisecond
	initialTokens         = 1.0
	maxTokensCap          = 32.0
)

// NewScheduler creates a Scheduler driven by the given monitor and starts
// its dispatch loop.
func NewScheduler(mon *Monitor) *Scheduler {
	s := &Scheduler{
		mon:            mon,
		wake:           make(chan struct{}, 1),
		pacingInterval: defaultPacingInterval,
	}
	s.cond = sync.NewCond(&s.mu)
	mon.SetOnChange(s.kick)
	go s.loop()
	return s
}

// kick wakes the dispatch loop without blocking; kicks coalesce, which is
// fine because the loop re-reads the full state after every wakeup.
func (s *Scheduler) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// SetPacingInterval tunes how fast admission tokens refill during recovery.
// Intended for tests; call before submitting tasks.
func (s *Scheduler) SetPacingInterval(d time.Duration) {
	s.mu.Lock()
	s.pacingInterval = d
	s.mu.Unlock()
}

// Submit enqueues a task for admission. It never rejects: backpressure is
// expressed by deferring admission, not by dropping work.
func (s *Scheduler) Submit(t *Task) {
	s.mu.Lock()
	s.queue = append(s.queue, t)
	s.mu.Unlock()
	s.kick()
}

// Wait blocks until the queue is drained and all running fibers finish.
func (s *Scheduler) Wait() {
	s.mu.Lock()
	for len(s.queue) > 0 || s.running > 0 {
		s.cond.Wait()
	}
	s.mu.Unlock()
}

// Close drains the queue and stops the dispatch loop.
func (s *Scheduler) Close() {
	s.Wait()
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	s.kick()
}

// Stats returns a snapshot of the scheduler counters.
func (s *Scheduler) Stats() (admitted, completed, blocked, episodes, violations int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Admitted, s.Completed, s.BlockedEvents, s.PressureEpisodes, s.Violations
}

// Pressured reports whether backpressure is currently engaged.
func (s *Scheduler) Pressured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pressured
}

// admission is a task plus the fiber reserved for it.
type admission struct {
	task  *Task
	fiber *Fiber
	gcc   bool
}

func (s *Scheduler) loop() {
	for {
		s.mu.Lock()
		if s.closed && len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		a := s.tryAdmitLocked()
		s.mu.Unlock()
		if a == nil {
			<-s.wake // sleep until accounting, queue or pacing changes
			continue
		}
		go s.run(a)
	}
}

// tryAdmitLocked attempts one admission. It returns nil (and counts a
// backpressure event, if work was pending) when nothing may run right now.
func (s *Scheduler) tryAdmitLocked() *admission {
	s.refreshPressureLocked()
	if len(s.queue) == 0 {
		return nil
	}
	if s.pressured {
		// Backpressure: hold back new (especially memory-hungry) fibers;
		// GC-capable fibers jump the queue and run without reservation,
		// their own Alloc blocks if the quota is exhausted.
		for _, t := range s.queue {
			if t.GCCapable {
				s.removeLocked(t)
				return s.admitLocked(t, newFiber(s.mon, newFiberID(), true, 0), true)
			}
		}
		s.BlockedEvents++
		return nil
	}
	if s.recovering && !s.takeTokenLocked() {
		s.BlockedEvents++
		return nil
	}
	t := s.queue[0]
	id := newFiberID()
	if !s.mon.tryAlloc(id, t.MemDemand) {
		s.BlockedEvents++
		return nil
	}
	s.removeLocked(t)
	return s.admitLocked(t, newFiber(s.mon, id, false, t.MemDemand), false)
}

func (s *Scheduler) admitLocked(t *Task, f *Fiber, gcc bool) *admission {
	s.Admitted++
	s.running++
	if s.pressured && !gcc {
		s.Violations++
	}
	return &admission{task: t, fiber: f, gcc: gcc}
}

// takeTokenLocked paces admissions during recovery: tokens refill over time
// and the bucket capacity doubles per refill, so the admission rate ramps up
// smoothly instead of bursting.
func (s *Scheduler) takeTokenLocked() bool {
	now := time.Now()
	if s.lastRefill.IsZero() {
		s.tokens = initialTokens
		s.maxTokens = 2 * initialTokens
		s.lastRefill = now
	} else if elapsed := now.Sub(s.lastRefill); elapsed >= s.pacingInterval {
		s.tokens += float64(elapsed / s.pacingInterval)
		s.maxTokens *= 2
		if s.maxTokens > maxTokensCap {
			s.maxTokens = maxTokensCap
		}
		if s.tokens > s.maxTokens {
			s.tokens = s.maxTokens
		}
		s.lastRefill = now
	}
	if s.tokens < 1 {
		s.armPacingTimerLocked()
		return false
	}
	s.tokens--
	if s.maxTokens >= maxTokensCap {
		s.recovering = false // fully ramped up
	}
	return true
}

func (s *Scheduler) armPacingTimerLocked() {
	if s.pacingTimer != nil {
		return
	}
	s.pacingTimer = time.AfterFunc(s.pacingInterval, func() {
		s.mu.Lock()
		s.pacingTimer = nil
		s.mu.Unlock()
		s.kick()
	})
}

func (s *Scheduler) run(a *admission) {
	a.task.Run(a.fiber)
	// Safety net: reclaim anything left, including unconsumed reservation.
	a.fiber.GC()

	s.mu.Lock()
	s.running--
	s.Completed++
	s.cond.Broadcast()
	s.mu.Unlock()
	s.kick()
}

// refreshPressureLocked updates the latched pressure/recovery state from the
// monitor's current usage.
func (s *Scheduler) refreshPressureLocked() {
	usage := s.mon.Usage()
	if !s.pressured && usage >= s.mon.HighWatermark() {
		s.pressured = true
		s.recovering = false
		s.PressureEpisodes++
	}
	if s.pressured && usage <= s.mon.LowWatermark() {
		s.pressured = false
		s.recovering = true
		s.lastRefill = time.Time{} // restart the token bucket
		s.tokens = 0
	}
}

func (s *Scheduler) removeLocked(t *Task) {
	for i, q := range s.queue {
		if q == t {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}
