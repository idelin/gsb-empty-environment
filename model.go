package main

import "time"

const (
	EventStart     = "start"
	EventEnd       = "end"
	EventReference = "reference"

	RefChildOf     = "child_of"
	RefFollowsFrom = "follows_from"
	ProblemError   = "error"
	ProblemWarning = "warning"
)

type Event struct {
	TraceID string
	SpanID  string
	Kind    string

	// At is local telemetry emission time. Causal validation uses the span
	// timestamps below, so late or reordered delivery does not alter causality.
	At           int64
	Start        int64
	End          int64
	RefType      string
	ParentSpanID string
	SpanIDRef    string
}

type Reference struct {
	Type   string
	Parent string
}

type Span struct {
	TraceID    string
	ID         string
	Start      int64
	End        int64
	References []Reference
}

func (s Span) Duration() time.Duration {
	return time.Duration(s.End-s.Start) * time.Nanosecond
}

type Problem struct {
	Code     string
	Severity string
	SpanID   string
	Message  string
}

type PathMetrics struct {
	Wall        time.Duration
	Work        time.Duration
	IdleWaiting time.Duration
	SpanIDs     []string
}

type Report struct {
	TraceID      string
	Status       string
	SpanCount    int
	ValidSpans   int
	TimelineWall time.Duration
	TotalWork    time.Duration
	Critical     PathMetrics
	Problems     []Problem
}

type ReplayError struct {
	Index   int
	TraceID string
	Message string
}

func (e ReplayError) Error() string {
	if e.TraceID == "" {
		return "event " + itoa(e.Index) + ": " + e.Message
	}
	return "event " + itoa(e.Index) + " trace " + e.TraceID + ": " + e.Message
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
