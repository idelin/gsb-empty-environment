package backtest

import (
	"fmt"
	"time"
)

// ReadRecord captures one audited read performed by the strategy: which
// event was read, which revision of it (identified by its arrival time),
// and the engine time at which the read happened.
type ReadRecord struct {
	EventID        string
	EventTime      time.Time // occurrence time of the revision read
	RevArrivalTime time.Time // arrival time of the revision read
	ReadAt         time.Time // engine time when the read occurred
	Found          bool
}

// Action is an irreversible decision emitted by the strategy. Actions are
// appended to an append-only log: a late correction may change what the
// strategy decides next, but it can never rewrite actions already taken.
type Action struct {
	Seq     int64
	At      time.Time // engine time when the action was taken
	Kind    string
	Detail  string
	Trigger string // ID of the event being processed when the action fired
}

// AuditLog is the append-only record of everything the strategy saw and
// did during a run.
type AuditLog struct {
	Reads   []ReadRecord
	Actions []Action
}

func (a *AuditLog) recordRead(r ReadRecord) {
	a.Reads = append(a.Reads, r)
}

func (a *AuditLog) recordAction(act Action) {
	a.Actions = append(a.Actions, act)
}

// ValidateReadSet proves the strategy never read future data: every
// revision it observed must have arrived no later than the moment it was
// read. Returns the first violation found, or nil.
func (a *AuditLog) ValidateReadSet() error {
	for i, r := range a.Reads {
		if !r.Found {
			continue
		}
		if r.RevArrivalTime.After(r.ReadAt) {
			return fmt.Errorf("read #%d: strategy read event %q revision that arrived at %s, but read happened at %s",
				i, r.EventID, r.RevArrivalTime, r.ReadAt)
		}
	}
	return nil
}

// EqualActions reports whether two action sequences are identical in
// order and content — the determinism criterion used by replay.
func EqualActions(x, y []Action) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		a, b := x[i], y[i]
		if a.Seq != b.Seq || a.Kind != b.Kind || a.Detail != b.Detail ||
			a.Trigger != b.Trigger || !a.At.Equal(b.At) {
			return false
		}
	}
	return true
}
