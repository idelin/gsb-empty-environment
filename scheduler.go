package scheduler

import (
	"context"
	"errors"
	"time"
)

// MissPolicy decides what happens to rounds whose scheduled time was
// missed because the previous run overran its period.
type MissPolicy int

const (
	// SkipMissed drops missed rounds; the next run is the first round
	// still in the future on the anchored schedule.
	SkipMissed MissPolicy = iota
	// CoalesceMissed merges all missed rounds into a single immediate
	// catch-up run (carrying the latest missed round ID), then resumes
	// the anchored schedule.
	CoalesceMissed
)

// Scheduler runs Task once per round. Round k is scheduled at
// Anchor + k*Period, independent of when the previous run finished.
type Scheduler struct {
	Clock  Clock
	Anchor time.Time
	Period time.Duration
	Policy MissPolicy
	// Task executes one round. The round ID is monotonically
	// increasing and never repeats, even across clock rewinds.
	Task func(ctx context.Context, round int64) error
	// OnError, if set, receives task failures; the schedule continues.
	OnError func(round int64, err error)
}

// Run executes rounds until ctx is canceled, returning ctx.Err().
// Waiting timers are released when Run returns.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.Period <= 0 {
		return errors.New("scheduler: Period must be positive")
	}
	if s.Task == nil {
		return errors.New("scheduler: Task must not be nil")
	}
	var last int64 = -1
	next := int64(0)
	for {
		if next <= last {
			next = last + 1
		}
		sched := s.Anchor.Add(time.Duration(next) * s.Period)
		if err := s.Clock.WaitUntil(ctx, sched); err != nil {
			return err
		}
		if err := s.Task(ctx, next); err != nil && s.OnError != nil {
			s.OnError(next, err)
		}
		last = next
		latestDue := int64(s.Clock.Now().Sub(s.Anchor) / s.Period)
		switch s.Policy {
		case SkipMissed:
			next = latestDue + 1
		case CoalesceMissed:
			if latestDue > last {
				next = latestDue
			} else {
				next = last + 1
			}
		}
	}
}
