// Package planner schedules database migration steps into phases,
// merges non-conflicting operations, minimizes write-stop windows,
// and marks irreversible boundaries.
package planner

import "fmt"

// MaxSteps is the hard limit on the number of steps in a migration plan.
const MaxSteps = 20

// Kind classifies a migration step.
type Kind string

const (
	KindAddColumn     Kind = "add_column"
	KindBackfill      Kind = "backfill"
	KindAddConstraint Kind = "add_constraint"
	KindCreateIndex   Kind = "create_index"
	KindDropColumn    Kind = "drop_column"
	KindDropTable     Kind = "drop_table"
	KindCustom        Kind = "custom"
)

// Object identifies a database object a step reads or writes.
// An empty Column means the whole table (covers every column of it).
type Object struct {
	Table  string `json:"table"`
	Column string `json:"column,omitempty"`
}

func (o Object) String() string {
	if o.Column == "" {
		return o.Table
	}
	return o.Table + "." + o.Column
}

// overlap reports whether two objects may refer to the same data.
func overlap(a, b Object) bool {
	if a.Table != b.Table {
		return false
	}
	return a.Column == "" || b.Column == "" || a.Column == b.Column
}

// Step is a single migration operation.
type Step struct {
	ID       string   `json:"id"`
	Kind     Kind     `json:"kind"`
	Table    string   `json:"table"`
	Column   string   `json:"column,omitempty"`
	Reads    []Object `json:"reads,omitempty"`
	Writes   []Object `json:"writes,omitempty"`
	Requires []string `json:"requires,omitempty"`
	// NeedsWriteStop marks steps that must run while application writes
	// are halted (e.g. constraint validation taking a strong lock).
	NeedsWriteStop bool `json:"needs_write_stop,omitempty"`
	// Rollback describes how to undo the step. Empty means no rollback
	// is available: the step is irreversible.
	Rollback string `json:"rollback,omitempty"`
}

// destructive reports whether the step destroys data by nature.
func (s Step) destructive() bool {
	return s.Kind == KindDropColumn || s.Kind == KindDropTable
}

// Irreversible reports whether the step cannot be rolled back.
func (s Step) Irreversible() bool {
	return s.Rollback == "" || s.destructive()
}

// applyDefaults fills Reads/Writes from the step kind when the caller
// left both empty, so conflict detection works out of the box.
func (s *Step) applyDefaults() {
	if s.Reads != nil || s.Writes != nil {
		return
	}
	switch s.Kind {
	case KindAddColumn, KindAddConstraint, KindDropColumn, KindDropTable:
		s.Writes = []Object{{Table: s.Table}}
		s.Reads = []Object{{Table: s.Table}}
	case KindBackfill:
		s.Reads = []Object{{Table: s.Table}}
		s.Writes = []Object{{Table: s.Table, Column: s.Column}}
	case KindCreateIndex:
		s.Writes = []Object{{Table: s.Table}}
	}
}

// conflicts reports whether two steps may not run in the same phase:
// one writes an object the other reads or writes.
func conflicts(a, b Step) bool {
	for _, w := range a.Writes {
		for _, o := range b.Writes {
			if overlap(w, o) {
				return true
			}
		}
		for _, o := range b.Reads {
			if overlap(w, o) {
				return true
			}
		}
	}
	for _, w := range b.Writes {
		for _, o := range a.Reads {
			if overlap(w, o) {
				return true
			}
		}
	}
	return false
}

// PlannedStep is a step placed into a phase.
type PlannedStep struct {
	Step       Step `json:"step"`
	Phase      int  `json:"phase"`
	WriteStop  bool `json:"write_stop"`
	Reversible bool `json:"reversible"`
}

// Phase is a set of steps that may execute together; every step in a
// phase depends only on steps in earlier phases and conflicts with no
// sibling in the same phase.
type Phase struct {
	Index     int           `json:"index"`
	WriteStop bool          `json:"write_stop"`
	Steps     []PlannedStep `json:"steps"`
}

// Boundary marks the point of no return: once the phase containing the
// first irreversible step has executed, a full rollback is impossible.
type Boundary struct {
	Phase  int    `json:"phase"`
	StepID string `json:"step_id"`
	Reason string `json:"reason"`
}

// Plan is the scheduled migration.
type Plan struct {
	Phases           []Phase   `json:"phases"`
	WriteStopWindows int       `json:"write_stop_windows"`
	Boundary         *Boundary `json:"irreversible_boundary,omitempty"`
	Warnings         []string  `json:"warnings,omitempty"`
}

// Error describes a planning failure.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...interface{}) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}
