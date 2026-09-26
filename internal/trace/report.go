package trace

// CriticalPath is the causal chain of spans that explains the
// end-to-end wall-clock latency of the trace.
type CriticalPath struct {
	Spans     []string // ordered span IDs, first starts the chain
	WallClock int64    // last.End - first.Start, in ms
	Work      int64    // sum of span durations on the chain, in ms
}

// Report is the outcome of analyzing one trace.
type Report struct {
	TraceID string

	SpanCount int
	WallClock int64 // max(End) - min(Start) over valid spans
	TotalWork int64 // sum of durations, each span counted once

	// CriticalPath is nil when a dependency cycle makes causal
	// ordering impossible; no fabricated path is emitted.
	CriticalPath *CriticalPath

	// Cycles lists span IDs involved in dependency cycles.
	Cycles []string

	// NegativeDurations lists span IDs with End < Start.
	NegativeDurations []string

	// CausalityViolations describes edges that break causal order
	// (child outside parent window, dependency inverted in time,
	// or references to unknown spans).
	CausalityViolations []string
}
