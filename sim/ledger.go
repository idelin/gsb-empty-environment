package sim

import "fmt"

type ActionKind int

const (
	ActionStopUpper ActionKind = iota
	ActionStopLower
	ActionCompensate
)

func (k ActionKind) String() string {
	switch k {
	case ActionStopUpper:
		return "stop-upper"
	case ActionStopLower:
		return "stop-lower"
	}
	return "compensate"
}

// ActionEntry is one versioned, immutable ledger record.
type ActionEntry struct {
	Version     int
	Kind        ActionKind
	Conclusion  Conclusion // conclusion that justified the entry
	Reason      string
	Compensates int // for ActionCompensate: version of the executed action
}

// Ledger is append-only. Executed irreversible actions are never rewritten;
// corrections appear only as later compensation entries.
type Ledger struct {
	entries []ActionEntry
}

func NewLedger() *Ledger { return &Ledger{} }

// Entries returns every recorded action version in order.
func (l *Ledger) Entries() []ActionEntry {
	return append([]ActionEntry(nil), l.entries...)
}

func (l *Ledger) append(kind ActionKind, c Conclusion, reason string, compensates int) ActionEntry {
	entry := ActionEntry{
		Version:     len(l.entries) + 1,
		Kind:        kind,
		Conclusion:  c,
		Reason:      reason,
		Compensates: compensates,
	}
	l.entries = append(l.entries, entry)
	return entry
}

// Execute records an irreversible stop action justified by a conclusion.
func (l *Ledger) Execute(c Conclusion) ActionEntry {
	kind := ActionStopUpper
	if c.Boundary == BoundaryLower {
		kind = ActionStopLower
	}
	reason := fmt.Sprintf("executed on first touch of %s edge at %+v", c.Boundary, c.Evidence)
	return l.append(kind, c, reason, 0)
}

// Compensate records a non-destructive correction suggestion targeting an
// already executed action. The target entry itself is left untouched.
func (l *Ledger) Compensate(target ActionEntry, c Conclusion, reason string) ActionEntry {
	return l.append(ActionCompensate, c, reason, target.Version)
}

// Executed returns only the irreversible stop actions, skipping
// compensation suggestions.
func (l *Ledger) Executed() []ActionEntry {
	var out []ActionEntry
	for _, entry := range l.entries {
		if entry.Kind != ActionCompensate {
			out = append(out, entry)
		}
	}
	return out
}
