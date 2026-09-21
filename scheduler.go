// Package scheduler runs a task at a fixed frequency anchored to a base
// time. The schedule is derived from the base time, never from when the
// previous run finished, so slow runs do not drift the cadence.
package scheduler

import (
	"context"
	"sync"
	"time"
)

// MissedPolicy decides how rounds missed while a run overruns are handled.
type MissedPolicy int

const (
	// SkipMissed drops missed rounds entirely; the next run is the first
	// round whose anchored schedule time is still in the future.
	SkipMissed MissedPolicy = iota
	// MergeMissed coalesces all missed rounds into a single run carrying
	// the latest missed round ID.
	MergeMissed
)

// Task is the unit of work executed once per round. round is the anchored
// round ID: the number of periods elapsed since Options.Base. The context
// carries the per-run timeout and is cancelled when the scheduler stops;
// tasks should honor it.
type Task func(ctx context.Context, round int64) error

// Options configures a Scheduler.
type Options struct {
	// Base anchors the schedule: round n fires at Base + n*Period.
	Base time.Time
	// Period is the fixed distance between rounds. Must be positive.
	Period time.Duration
	// ExecTimeout bounds a single run; 0 disables the timeout. A run that
	// exceeds it is abandoned and reported via OnError with
	// context.DeadlineExceeded.
	ExecTimeout time.Duration
	// Policy selects how rounds missed during an overrun are handled.
	Policy MissedPolicy
	// OnError, if set, reports task failures and timeouts.
	OnError func(round int64, err error)
}

// Scheduler runs a Task at a fixed frequency. It is safe for concurrent
// use; Start and Stop must each be called at most once per run cycle.
type Scheduler struct {
	clock Clock
	task  Task
	opts  Options

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	lastRound int64 // highest round ID already executed; -1 before the first run
}

// New creates a Scheduler. The clock and task are injected, which lets tests
// supply a FakeClock, a stub task and controlled failures.
func New(clock Clock, task Task, opts Options) *Scheduler {
	if opts.Period <= 0 {
		panic("scheduler: Period must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		clock:     clock,
		task:      task,
		opts:      opts,
		ctx:       ctx,
		cancel:    cancel,
		lastRound: -1,
	}
}

// Start launches the scheduling loop in a background goroutine.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.loop()
}

// Stop cancels the scheduler, disposes of the pending timer and waits for
// the loop to exit.
func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}

// LastRound returns the highest round ID executed so far, or -1.
func (s *Scheduler) LastRound() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRound
}

// roundAt maps a timestamp to its anchored round ID using floor division,
// so timestamps before Base map to negative rounds.
func (s *Scheduler) roundAt(t time.Time) int64 {
	d := t.Sub(s.opts.Base)
	q := int64(d / s.opts.Period)
	if d%s.opts.Period != 0 && d < 0 {
		q--
	}
	return q
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	for {
		now := s.clock.Now()

		s.mu.Lock()
		next := s.lastRound + 1
		s.mu.Unlock()

		// A round is overdue only when its anchored time is strictly in
		// the past; a round due exactly now still runs. next is derived
		// from lastRound, not from the wall clock, so a clock rollback
		// can never select an already-executed round.
		if at := s.opts.Base.Add(time.Duration(next) * s.opts.Period); at.Before(now) {
			due := s.roundAt(now)
			if s.opts.Policy == MergeMissed {
				// Coalesce every missed round into one run.
				if !s.execute(due) {
					return
				}
				continue
			}
			// SkipMissed: drop overdue rounds, schedule the first
			// future round anchored to Base.
			next = due + 1
		}

		at := s.opts.Base.Add(time.Duration(next) * s.opts.Period)
		wait := at.Sub(now)
		if wait < 0 {
			wait = 0
		}
		timer := s.clock.NewTimer(wait)
		select {
		case <-s.ctx.Done():
			timer.Stop() // release the timer on cancellation
			return
		case <-timer.C():
		}
		if !s.execute(next) {
			return
		}
	}
}

// execute runs one round and records it as executed. It returns false when
// the scheduler was stopped mid-run.
func (s *Scheduler) execute(round int64) bool {
	s.mu.Lock()
	if round <= s.lastRound {
		// Defensive guard: never execute the same round twice, even if
		// the clock jumped backwards between scheduling and firing.
		s.mu.Unlock()
		return true
	}
	s.lastRound = round
	s.mu.Unlock()

	ctx := s.ctx
	cancel := context.CancelFunc(func() {})
	if s.opts.ExecTimeout > 0 {
		ctx, cancel = context.WithTimeout(s.ctx, s.opts.ExecTimeout)
	}
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.task(ctx, round) }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		// Timeout or Stop. A task that ignores ctx keeps running in the
		// background but no longer blocks the schedule.
		err = ctx.Err()
	}
	if err != nil && s.opts.OnError != nil {
		s.opts.OnError(round, err)
	}
	return s.ctx.Err() == nil
}
