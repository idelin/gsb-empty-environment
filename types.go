// Package netsec implements a small, in-memory trigger execution model for
// database test hosts.
//
// The model is deliberately explicit about the security boundaries that a
// database trigger can cross: security invoker/definer execution, definer
// writable-table whitelists, statement-scoped re-entrancy, trigger recursion
// depth, and statement rollback (including audit writes).
package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Event is a DML event understood by the trigger engine.
type Event string

const (
	EventInsert Event = "INSERT"
	EventUpdate Event = "UPDATE"
	EventDelete Event = "DELETE"

	// EventAny can be used in a Trigger to make it fire for all DML events.
	EventAny Event = "*"
)

// Aliases kept for callers that prefer the shorter spelling.
const (
	Insert = EventInsert
	Update = EventUpdate
	Delete = EventDelete
	Any    = EventAny

	InsertEvent = EventInsert
	UpdateEvent = EventUpdate
	DeleteEvent = EventDelete
)

// ParseEvent normalises the common spellings of a DML event.
func ParseEvent(s string) (Event, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "INSERT", "I":
		return EventInsert, nil
	case "UPDATE", "U":
		return EventUpdate, nil
	case "DELETE", "D":
		return EventDelete, nil
	case "*", "ANY", "ALL":
		return EventAny, nil
	default:
		return "", fmt.Errorf("netsec: unknown event %q", s)
	}
}

func normaliseEvent(e Event) Event {
	if e == "" {
		return EventAny
	}
	if pe, err := ParseEvent(string(e)); err == nil {
		return pe
	}
	return Event(strings.ToUpper(strings.TrimSpace(string(e))))
}

// Timing is the point at which a trigger runs relative to the base-table write.
type Timing string

const (
	TimingBefore Timing = "BEFORE"
	TimingAfter  Timing = "AFTER"
)

// Aliases kept for callers that prefer the shorter spelling.
const (
	Before = TimingBefore
	After  = TimingAfter

	BeforeTrigger = TimingBefore
	AfterTrigger  = TimingAfter
)

func normaliseTiming(t Timing) Timing {
	switch strings.ToUpper(strings.TrimSpace(string(t))) {
	case "AFTER":
		return TimingAfter
	default:
		return TimingBefore
	}
}

// Security is a trigger's SQL security context.
type Security string

const (
	SecurityInvoker Security = "INVOKER"
	SecurityDefiner Security = "DEFINER"
)

const (
	Invoker = SecurityInvoker
	Definer = SecurityDefiner

	InvokerSecurity = SecurityInvoker
	DefinerSecurity = SecurityDefiner
)

func normaliseSecurity(s Security) Security {
	switch strings.ToUpper(strings.TrimSpace(string(s))) {
	case "DEFINER":
		return SecurityDefiner
	default:
		return SecurityInvoker
	}
}

// RowID identifies a row within a table. String IDs keep the test host simple
// while still allowing callers to synthesise IDs from other values.
type RowID string

// ID is a short alias for RowIDOf.
func ID(v any) RowID { return RowIDOf(v) }

// RowIDOf converts any scalar value into a RowID.
func RowIDOf(v any) RowID {
	switch x := v.(type) {
	case RowID:
		return x
	case string:
		return RowID(x)
	case fmt.Stringer:
		return RowID(x.String())
	default:
		return RowID(fmt.Sprint(v))
	}
}

// Row is an unordered map of column names to values. Callers should use
// CloneRow whenever they intend to mutate a row obtained from the engine.
type Row map[string]any

// CloneRow makes a shallow copy of a row. Values are copied by assignment; the
// engine's own rows are always cloned before they are exposed to trigger code
// or written, so mutating nested values is not visible until written back.
func CloneRow(r Row) Row {
	if r == nil {
		return nil
	}
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// DML is one data-modification operation. For Update, Row is the new row. For
// Insert, Row is the inserted row. For Delete, Row may be nil; the engine uses
// the existing row for policy decisions.
type DML struct {
	Event Event
	Table string
	RowID RowID
	Row   Row
}

// Action is a declarative DML performed by a trigger. It is used both by the
// runtime and by the differential trigger-chain search. Actions execute in the
// declared order after the trigger's Modify/Before callback and Body.
type Action struct {
	Event Event
	Table string
	RowID RowID
	Row   Row
}

// DML converts an Action to a DML request.
func (a Action) DML() DML {
	return DML{Event: normaliseEvent(a.Event), Table: a.Table, RowID: a.RowID, Row: CloneRow(a.Row)}
}

// Trigger is registered on a table and runs for one or more DML events.
type Trigger struct {
	// ID is unique across the whole engine. It is used in frames and in the
	// statement-scoped (trigger, row) de-duplication rule.
	ID string

	// Table is filled in when the trigger is registered. It lets
	// Engine.RegisterTrigger find the owning table.
	Table string

	// Event restricts the trigger. EventAny (or an empty value) fires for all
	// DML events.
	Event Event

	// Timing is normally inferred when the trigger is registered with
	// AddBefore/AddAfter, but it is recorded in frames for consistency.
	Timing Timing

	// Security selects whether the trigger runs with the invoker's effective
	// privileges or the definer's.
	Security Security

	// Definer is the owner subject used by a definer trigger.
	Definer string

	// WritableTables is the definer's declared whitelist. A definer trigger is
	// only permitted to act as the definer on these tables.
	WritableTables []string

	// MaxDepth caps the nesting depth at which this trigger may fire. Zero
	// means only the engine-wide depth applies.
	MaxDepth int

	// When is evaluated against the current row before the trigger runs. If it
	// returns false the trigger is skipped (but is not considered to have run
	// for the per-row rule).
	When func(ctx *TriggerContext, row Row) bool

	// Modify runs for BEFORE triggers and may return a changed row. Returning
	// an error rejects the statement. For AFTER triggers it is ignored.
	Modify func(ctx *TriggerContext, row Row) (Row, error)

	// Body performs arbitrary trigger work through the TriggerContext. Any
	// nested DML issued by Body belongs to the same statement.
	Body func(ctx *TriggerContext) error

	// Actions is a declarative body, useful for the BFS explorer. The runtime
	// executes every action in order; the explorer branches over them as
	// possible next DML steps.
	Actions []Action
}

func (t *Trigger) matches(event Event) bool {
	ev := normaliseEvent(t.Event)
	return ev == EventAny || ev == event
}

func (t *Trigger) security() Security {
	return normaliseSecurity(t.Security)
}

// Frame is the execution context pushed for every trigger invocation.
type Frame struct {
	TriggerID string

	// Table is the table whose DML fired this trigger. RowID identifies the
	// row in that table.
	Table      string
	EventTable string
	RowID      RowID
	Event      Event

	EffectiveSubject string
	InvokerSubject   string
	OriginSubject    string
	StatementID      string

	Timing   Timing
	Security Security
	Definer  string
	Depth    int

	WritableTables []string

	RootEvent Event
	RootTable string
	RootRowID RowID
}

// Clone copies a frame and its slice fields.
func (f Frame) Clone() Frame {
	f.WritableTables = append([]string(nil), f.WritableTables...)
	return f
}

// String renders the security-relevant context of one frame.
func (f Frame) String() string {
	return fmt.Sprintf(
		"frame{trigger=%s event=%s table=%s row=%s effective=%s invoker=%s origin=%s stmt=%s timing=%s security=%s definer=%s depth=%d writable=[%s]}",
		f.TriggerID, f.Event, f.Table, f.RowID, f.EffectiveSubject,
		f.InvokerSubject, f.OriginSubject, f.StatementID, f.Timing,
		f.Security, f.Definer, f.Depth, strings.Join(f.WritableTables, ","),
	)
}

// WriteRecord describes one successful base-table write. The trigger that
// caused a nested write is available in Frame.TriggerID (empty for the
// statement's root DML).
type WriteRecord struct {
	Event       Event
	Table       string
	RowID       RowID
	Row         Row
	Subject     string
	Origin      string
	StatementID string
	TriggerID   string
	Depth       int
}

func (w WriteRecord) String() string {
	return fmt.Sprintf(
		"write{event=%s table=%s row=%s subject=%s origin=%s stmt=%s trigger=%s}",
		w.Event, w.Table, w.RowID, w.Subject, w.Origin, w.StatementID, w.TriggerID,
	)
}

// Result is the outcome of one statement.
type Result struct {
	StatementID string
	Origin      string
	Frames      []Frame
	Writes      []WriteRecord
	RolledBack  bool
	Err         error
}

// FramesByTrigger returns a stable, trigger-ID-ordered copy.
func FramesByTrigger(frames []Frame) []Frame {
	out := append([]Frame(nil), frames...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].TriggerID < out[j].TriggerID })
	return out
}
