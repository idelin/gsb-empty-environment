package traceasm

import "time"

// Span is a single telemetry span as ingested from a local fixture.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string // empty means root
	Name         string
	Start        time.Time
	End          time.Time
}

// EventType classifies the events emitted by the Assembler.
type EventType string

const (
	EventRootRegistered   EventType = "RootRegistered"
	EventSpanAttached     EventType = "SpanAttached"
	EventOrphanPending    EventType = "OrphanPending"
	EventOrphanResolved   EventType = "OrphanResolved"
	EventOrphanExpired    EventType = "OrphanExpired"
	EventDuplicateUpdated EventType = "DuplicateUpdated"
	EventConflict         EventType = "ConflictDetected"
	EventCycle            EventType = "CycleDetected"
	EventCrossTraceRef    EventType = "CrossTraceReference"
)

// Event is emitted for every structural decision the Assembler makes.
type Event struct {
	Type     EventType
	TraceID  string
	SpanID   string
	ParentID string
	Detail   string
	At       time.Time
}
