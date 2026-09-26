package tailsampler

import "time"

// Span is the minimal telemetry unit the sampler understands.
type Span struct {
	TraceID  string
	SpanID   string
	ParentID string // empty means root span
	Name     string
	Start    time.Time
	End      time.Time
	Error    bool
}

func (s Span) IsRoot() bool { return s.ParentID == "" }

func (s Span) Duration() time.Duration { return s.End.Sub(s.Start) }
