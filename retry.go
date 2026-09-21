// Package retrysched schedules failing tasks for retry following an explicit
// backoff sequence. The clock is injected so tests can drive time manually.
package retrysched

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCanceled is the result error of a task canceled before completion.
var ErrCanceled = errors.New("retrysched: task canceled")

// Clock abstracts time so tests can inject a manual clock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a schedulable callback that can be stopped before firing.
type Timer interface {
	// Stop reports whether the timer was still pending.
	Stop() bool
}

// RealClock implements Clock with the time package.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return time.AfterFunc(d, f)
}

// ManualClock is a test clock. Timers fire only when Advance moves time past
// their deadline; firing happens synchronously in the caller's goroutine.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*manualTimer]struct{}
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start, timers: make(map[*manualTimer]struct{})}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &manualTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers[t] = struct{}{}
	return t
}

// Pending reports how many timers are still scheduled.
func (c *ManualClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// Advance moves the clock forward and fires every timer that comes due,
// including timers scheduled by callbacks within the advanced window. The
// clock stops at each fired timer's deadline, so callbacks observe the
// correct Now and may schedule further timers inside the window.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var due *manualTimer
		for t := range c.timers {
			if !t.at.After(target) && (due == nil || t.at.Before(due.at)) {
				due = t
			}
		}
		if due == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		delete(c.timers, due)
		c.now = due.at
		c.mu.Unlock()
		due.f()
	}
}

type manualTimer struct {
	clock *ManualClock
	at    time.Time
	f     func()
}

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if _, ok := t.clock.timers[t]; !ok {
		return false
	}
	delete(t.clock.timers, t)
	return true
}

// Attempt records one execution of the task function.
type Attempt struct {
	Attempt int
	At      time.Time
	Err     error
}

// Result is the terminal outcome of a task. Exactly one Result is ever
// produced per task, even when cancel races a firing retry timer.
type Result struct {
	ID       string
	Attempts []Attempt
	// Err is nil on success, the last attempt error when the backoff
	// sequence is exhausted, or ErrCanceled when canceled.
	Err error
}

// TaskFunc executes one attempt. attempt starts at 1. The context is
// canceled if the task is canceled while the attempt is running.
type TaskFunc func(ctx context.Context, attempt int) error

type taskState int

const (
	stateRunning taskState = iota
	stateBackoff
	stateDone
)

type task struct {
	id       string
	fn       TaskFunc
	state    taskState
	attempt  int
	history  []Attempt
	timer    Timer
	cancel   context.CancelFunc
	canceled bool
	result   Result
	doneCh   chan struct{}
}

func (t *task) finishLocked(r Result) {
	t.state = stateDone
	t.result = r
	close(t.doneCh)
}

// Scheduler retries failed tasks along an explicit backoff sequence.
type Scheduler struct {
	clock   Clock
	backoff []time.Duration

	mu    sync.Mutex
	tasks map[string]*task
}

// NewScheduler returns a Scheduler that waits backoff[i] before attempt i+2.
// A task therefore runs at most len(backoff)+1 attempts.
func NewScheduler(clock Clock, backoff []time.Duration) *Scheduler {
	return &Scheduler{clock: clock, backoff: backoff, tasks: make(map[string]*task)}
}

// Handle tracks one submitted task.
type Handle struct {
	s *Scheduler
	t *task
}

// Submit registers id and starts attempt 1 in a new goroutine.
func (s *Scheduler) Submit(id string, fn TaskFunc) *Handle {
	t := &task{id: id, fn: fn, doneCh: make(chan struct{})}
	s.mu.Lock()
	s.tasks[id] = t
	s.mu.Unlock()
	go s.runAttempt(t)
	return &Handle{s: s, t: t}
}

// Wait blocks until the task reaches its single terminal result.
func (h *Handle) Wait() Result {
	<-h.t.doneCh
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	return h.t.result
}

// Cancel cancels the task. It reports whether the cancel was accepted, i.e.
// the task was not already done. Cancel during a backoff wait stops the
// pending timer and finishes immediately without waiting for it.
func (h *Handle) Cancel() bool {
	s := h.s
	t := h.t
	s.mu.Lock()
	defer s.mu.Unlock()
	switch t.state {
	case stateDone:
		return false
	case stateBackoff:
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		t.finishLocked(Result{ID: t.id, Attempts: t.history, Err: ErrCanceled})
		return true
	default: // stateRunning
		t.canceled = true
		if t.cancel != nil {
			t.cancel()
		}
		return true
	}
}

func (s *Scheduler) runAttempt(t *task) {
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	if t.state == stateDone {
		// Lost the race: cancel (or another terminal transition) won while
		// this attempt was being started. Produce no further result.
		s.mu.Unlock()
		cancel()
		return
	}
	t.state = stateRunning
	t.cancel = cancel
	t.attempt++
	attempt := t.attempt
	s.mu.Unlock()

	err := t.fn(ctx, attempt)

	s.mu.Lock()
	defer s.mu.Unlock()
	cancel()
	t.cancel = nil
	t.history = append(t.history, Attempt{Attempt: attempt, At: s.clock.Now(), Err: err})
	switch {
	case t.canceled:
		t.finishLocked(Result{ID: t.id, Attempts: t.history, Err: ErrCanceled})
	case err == nil:
		t.finishLocked(Result{ID: t.id, Attempts: t.history, Err: nil})
	case attempt > len(s.backoff):
		// Backoff sequence exhausted: report the last error plus history.
		t.finishLocked(Result{ID: t.id, Attempts: t.history, Err: err})
	default:
		delay := s.backoff[attempt-1]
		t.state = stateBackoff
		t.timer = s.clock.AfterFunc(delay, func() { s.onTimer(t) })
	}
}

// onTimer fires when a backoff wait expires. If the task already finished
// (success, exhaustion, or cancel), the late timer must not start a retry.
func (s *Scheduler) onTimer(t *task) {
	s.mu.Lock()
	if t.state != stateBackoff {
		s.mu.Unlock()
		return
	}
	t.timer = nil
	s.mu.Unlock()
	s.runAttempt(t)
}
