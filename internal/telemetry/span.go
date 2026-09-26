// Package telemetry defines the span model and a local, deterministic
// telemetry synthesizer. No external monitoring platform is involved:
// events are produced locally and can be replayed from fixtures.
package telemetry

// Kind is the span role in a request.
type Kind string

const (
	KindClient Kind = "client"
	KindServer Kind = "server"
)

// Status is the terminal outcome of a span.
type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// Span is one telemetry event. Client and server spans that belong to the
// same request attempt share TraceID and RequestID. A retry gets a new
// RequestID and an incremented Attempt number.
type Span struct {
	TraceID       string `json:"trace_id"`
	SpanID        string `json:"span_id"`
	Kind          Kind   `json:"kind"`
	Service       string `json:"service"` // may be empty; never guessed
	Operation     string `json:"operation"`
	RequestID     string `json:"request_id"`
	Attempt       int    `json:"attempt"`
	Status        Status `json:"status"`
	StartUnixNano int64  `json:"start_unix_nano"`
	DurationNano  int64  `json:"duration_nano"`
}
