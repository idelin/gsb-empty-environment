package logrec

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Telemetry event kinds. Events are synthesized locally by the
// assembler; nothing is shipped to an external monitoring platform.
const (
	EvGapDetected       = "gap_detected"        // sequence numbers skipped
	EvDuplicateLine     = "duplicate_line"      // same seq, same text, ignored
	EvLineCorrected     = "line_corrected"      // same seq, new text: revision
	EvLateContinuation  = "late_continuation"   // late line merged into a closed record
	EvLateStartLine     = "late_start_line"     // out-of-order start line opens own record
	EvOrphanContinued   = "orphan_continuation" // continuation with no open record
	EvRecordTruncated   = "record_truncated"    // record exceeded its size cap
	EvBackpressure      = "backpressure"        // buffer budget exhausted, feed rejected
	EvRulesSwapped      = "rules_swapped"       // assembly rules replaced at runtime
	EvRecordClosed      = "record_closed"       // record emitted
	EvSourceClosed      = "source_closed"       // source/epoch stream ended
	EvCheckpointSaved   = "checkpoint_saved"
	EvCheckpointRestore = "checkpoint_restored"
)

// Event is one telemetry data point.
type Event struct {
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"`
	Epoch    uint64 `json:"epoch,omitempty"`
	Seq      uint64 `json:"seq,omitempty"`
	RecordID string `json:"record_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func (e Event) String() string {
	return fmt.Sprintf("%s src=%s epoch=%d seq=%d rec=%s %s",
		e.Kind, e.Source, e.Epoch, e.Seq, e.RecordID, e.Detail)
}

// Telemetry is an in-memory, replayable event sink. It is safe for
// concurrent use.
type Telemetry struct {
	mu     sync.Mutex
	events []Event
}

// NewTelemetry returns an empty sink.
func NewTelemetry() *Telemetry { return &Telemetry{} }

// Emit appends an event.
func (t *Telemetry) Emit(e Event) {
	t.mu.Lock()
	t.events = append(t.events, e)
	t.mu.Unlock()
}

// Events returns a copy of all recorded events in order.
func (t *Telemetry) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// Kinds returns only the event kind strings, handy for assertions.
func (t *Telemetry) Kinds() []string {
	evs := t.Events()
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

// Count returns how many events of a kind were recorded.
func (t *Telemetry) Count(kind string) int {
	n := 0
	for _, e := range t.Events() {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// WriteJSONL serializes the event log as JSON lines, e.g. to build a
// replayable fixture file.
func (t *Telemetry) WriteJSONL(w io.Writer) error {
	enc := json.NewEncoder(w)
	for _, e := range t.Events() {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
