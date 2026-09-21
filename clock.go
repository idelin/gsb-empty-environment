// Package scheduler provides a fixed-frequency task scheduler whose
// schedule is anchored to a base time rather than to the end of the
// previous run.
package scheduler

import (
	"context"
	"time"
)

// Clock abstracts time so tests can inject a controllable clock.
type Clock interface {
	Now() time.Time
	// WaitUntil blocks until the clock reaches t or ctx is canceled.
	// Implementations must release any timer resources on cancellation.
	WaitUntil(ctx context.Context, t time.Time) error
}

// RealClock is a Clock backed by the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) WaitUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
