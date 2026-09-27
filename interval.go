package clocksync

import (
	"fmt"
	"math"
)

// Interval is a closed feasible range for a clock offset.
// Lo/Hi may be -Inf/+Inf when constraints are insufficient.
type Interval struct {
	Lo, Hi float64
}

// Wide returns the unconstrained interval [-inf, +inf].
func Wide() Interval { return Interval{math.Inf(-1), math.Inf(1)} }

// Empty reports whether the interval is infeasible.
func (i Interval) Empty() bool { return i.Lo > i.Hi }

// Intersect returns the intersection of two intervals.
func Intersect(a, b Interval) Interval {
	return Interval{math.Max(a.Lo, b.Lo), math.Min(a.Hi, b.Hi)}
}

func (i Interval) String() string {
	return fmt.Sprintf("[%s, %s]", formatBound(i.Lo), formatBound(i.Hi))
}

func formatBound(v float64) string {
	switch {
	case math.IsInf(v, -1):
		return "-inf"
	case math.IsInf(v, 1):
		return "+inf"
	default:
		return fmt.Sprintf("%+.3f", v)
	}
}
