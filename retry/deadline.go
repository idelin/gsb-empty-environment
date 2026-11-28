package retry

import "time"

// Deadline carries the propagated end-to-end deadline of a logical call.
type Deadline struct {
	// End is the absolute deadline. The zero value means "no deadline".
	End time.Time
}

// NewDeadline builds an absolute deadline from now plus a budget.
func NewDeadline(now time.Time, budget time.Duration) Deadline {
	if budget <= 0 {
		return Deadline{}
	}
	return Deadline{End: now.Add(budget)}
}

// Set returns a new deadline ending at now plus budget, implementing
// remaining-deadline propagation across a hop.
func (d Deadline) Set(now time.Time, budget time.Duration) Deadline {
	return NewDeadline(now, budget)
}

// HasDeadline reports whether a deadline is configured.
func (d Deadline) HasDeadline() bool { return !d.End.IsZero() }

// Remaining returns remainingDeadline = deadline - elapsed at now. A missing
// deadline is represented by a non-positive, huge duration flag handled by
// callers through HasDeadline.
func (d Deadline) Remaining(now time.Time) time.Duration {
	if !d.HasDeadline() {
		return time.Duration(1 << 62)
	}
	return d.End.Sub(now)
}

// Expired reports whether the deadline already passed at now.
func (d Deadline) Expired(now time.Time) bool {
	return d.HasDeadline() && !d.End.After(now)
}

// EnoughFor implements the mandatory retry pre-check:
//
//	remaining > minDownstreamProcessing + backoffWait
//
// A missing deadline always passes. Equality does NOT pass (the requirement
// is strictly greater than), so a retry that cannot complete in time is
// aborted instead of doing useless work.
func (d Deadline) EnoughFor(now time.Time, minProcessing, backoffWait time.Duration) bool {
	if !d.HasDeadline() {
		return true
	}
	return d.Remaining(now) > minProcessing+backoffWait
}
