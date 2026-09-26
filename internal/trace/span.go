// Package trace analyzes tracing DAGs: it computes the wall-clock
// critical path, accumulated work, and reports data anomalies.
package trace

// Span is a single unit of work. Times are milliseconds since an
// arbitrary trace-local epoch.
type Span struct {
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
	Name   string `json:"name"`
	Start  int64  `json:"start_ms"`
	End    int64  `json:"end_ms"`
}

// Duration of the span in milliseconds. May be negative for
// malformed telemetry; such spans are reported as anomalies.
func (s Span) Duration() int64 { return s.End - s.Start }

// Dependency is an asynchronous causal edge: To must not start
// before From has finished.
type Dependency struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Trace is a replayable fixture of synthesized telemetry.
type Trace struct {
	ID           string       `json:"trace_id"`
	Spans        []Span       `json:"spans"`
	Dependencies []Dependency `json:"dependencies"`
}
