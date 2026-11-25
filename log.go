package parity

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// EventKind labels a safe log event.
type EventKind string

const (
	EventQuery      EventKind = "query"
	EventOutcome    EventKind = "outcome"
	EventDerivation EventKind = "derivation"
	EventCheckpoint EventKind = "checkpoint"
	EventTerminate  EventKind = "terminate"
)

// Event is the ONLY thing passed to a Logger. It contains observable queries
// and derived interval state. It has no field for callback-internal plaintext.
type Event struct {
	Kind       EventKind `json:"kind"`
	Seq        int       `json:"seq"`
	Step       int       `json:"step,omitempty"`
	X          string    `json:"x,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Lower      string    `json:"lower,omitempty"`
	Upper      string    `json:"upper,omitempty"`
	Intervals  int       `json:"intervals,omitempty"`
	Candidates string    `json:"candidates,omitempty"`
	Queries    int       `json:"queries,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// Logger receives safe Events. Implementations must be safe for concurrent use
// if the host shares one across runs; the attack itself is sequential.
type Logger interface {
	LogEvent(Event)
}

// LoggerFunc adapts a function to a Logger.
type LoggerFunc func(Event)

// LogEvent implements Logger.
func (f LoggerFunc) LogEvent(ev Event) { f(ev) }

// NopLogger discards all events.
func NopLogger() Logger { return LoggerFunc(func(Event) {}) }

// TextLogger writes stable single-line key=value records to an io.Writer.
type TextLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewTextLogger creates a line logger.
func NewTextLogger(w io.Writer) *TextLogger { return &TextLogger{w: w} }

// LogEvent writes one deterministic-ish line.
func (l *TextLogger) LogEvent(ev Event) {
	if l == nil || l.w == nil {
		return
	}
	keys := []string{"kind", "seq", "step", "x", "outcome", "lower", "upper", "intervals", "candidates", "queries", "message"}
	vals := map[string]string{
		"kind":       string(ev.Kind),
		"seq":        itoa(ev.Seq),
		"step":       itoa(ev.Step),
		"x":          ev.X,
		"outcome":    ev.Outcome,
		"lower":      ev.Lower,
		"upper":      ev.Upper,
		"intervals":  itoa(ev.Intervals),
		"candidates": ev.Candidates,
		"queries":    itoa(ev.Queries),
		"message":    ev.Message,
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := vals[k]
		if v == "" || (k == "step" && ev.Step == 0) {
			continue
		}
		if strings.ContainsAny(v, " \t\n\"") {
			v = fmt.Sprintf("%q", v)
		}
		parts = append(parts, k+"="+v)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, strings.Join(parts, " "))
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }

// RecordingLogger collects events in memory for host assertions.
type RecordingLogger struct {
	mu     sync.Mutex
	events []Event
}

// NewRecordingLogger returns an in-memory event sink.
func NewRecordingLogger() *RecordingLogger { return &RecordingLogger{} }

// LogEvent records an event.
func (r *RecordingLogger) LogEvent(ev Event) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// Events returns a copy of recorded events.
func (r *RecordingLogger) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// SortedKeys is a small helper retained for hosts that build custom logs.
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
