package tailsampler

import "time"

// TraceView is the aggregated state of one trace handed to policies.
type TraceView struct {
	TraceID  string
	Spans    []Span
	HasError bool
	Duration time.Duration // max(End) - min(Start) across buffered spans
	Complete bool          // root span observed
	TimedOut bool          // decided by the timeout sweep, not by completion
}

// Policy evaluates an aggregated trace into a keep/drop decision.
type Policy interface {
	Evaluate(t TraceView) Decision
}

// CompositePolicy decides completed traces by error and latency signals, and
// delegates incomplete (timed out or evicted) traces to a separate
// TimeoutPolicy.
type CompositePolicy struct {
	// LatencyThreshold keeps completed traces whose end-to-end duration is
	// at least this long. Zero disables the latency rule.
	LatencyThreshold time.Duration
	// TimeoutPolicy handles traces that never completed in time. If nil,
	// incomplete traces are dropped.
	TimeoutPolicy Policy
}

func (p CompositePolicy) Evaluate(t TraceView) Decision {
	// A single error span is enough to keep the whole trace, complete or not.
	if t.HasError {
		if t.TimedOut {
			return keep("timeout_error")
		}
		return keep("error")
	}
	if !t.Complete {
		if p.TimeoutPolicy != nil {
			return p.TimeoutPolicy.Evaluate(t)
		}
		return drop("timeout")
	}
	if p.LatencyThreshold > 0 && t.Duration >= p.LatencyThreshold {
		return keep("slow_trace")
	}
	return drop("no_signal")
}

// TimeoutKeepPolicy is the standalone policy for traces that did not finish
// before the timeout: keep them when they already look slow, drop otherwise.
// (Error traces never reach this policy; CompositePolicy keeps them first.)
type TimeoutKeepPolicy struct {
	LatencyThreshold time.Duration
}

func (p TimeoutKeepPolicy) Evaluate(t TraceView) Decision {
	if p.LatencyThreshold > 0 && t.Duration >= p.LatencyThreshold {
		return keep("timeout_slow")
	}
	return drop("timeout")
}
