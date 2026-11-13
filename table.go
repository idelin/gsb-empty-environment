package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Table is an in-memory table plus its ordered trigger registrations.
//
// A nil Policy denies all writes. Use AllowAll explicitly for permissive test
// fixtures.
type Table struct {
	Name   string
	Policy Policy
	Rows   map[RowID]Row

	Before []*Trigger
	After  []*Trigger
}

// NewTable constructs a table.
func NewTable(name string) *Table {
	return &Table{Name: name, Rows: make(map[RowID]Row)}
}

// AddRow inserts or replaces a row. It is intended for test setup, not for
// trigger execution (use the engine so writes are journaled).
func (t *Table) AddRow(id RowID, row Row) *Table {
	if t.Rows == nil {
		t.Rows = make(map[RowID]Row)
	}
	t.Rows[id] = CloneRow(row)
	return t
}

// Row returns a copy of the named row.
func (t *Table) Row(id RowID) (Row, bool) {
	r, ok := t.Rows[id]
	if !ok {
		return nil, false
	}
	return CloneRow(r), true
}

// AddBefore appends an ordered BEFORE trigger.
func (t *Table) AddBefore(tr *Trigger) error {
	return t.addTrigger(TimingBefore, tr)
}

// AddAfter appends an ordered AFTER trigger.
func (t *Table) AddAfter(tr *Trigger) error {
	return t.addTrigger(TimingAfter, tr)
}

func (t *Table) addTrigger(timing Timing, tr *Trigger) error {
	if tr == nil {
		return fmt.Errorf("netsec: nil trigger")
	}
	if tr.ID == "" {
		return fmt.Errorf("netsec: trigger on %s has an empty ID", t.Name)
	}
	if tr.Table != "" && tr.Table != t.Name {
		return fmt.Errorf("netsec: trigger %s belongs to %s, not %s", tr.ID, tr.Table, t.Name)
	}
	if tr.Timing != "" {
		if normaliseTiming(tr.Timing) != timing {
			return fmt.Errorf("netsec: trigger %s is %s but registered as %s", tr.ID, tr.Timing, timing)
		}
	} else {
		tr.Timing = timing
	}
	tr.Table = t.Name
	if t.Before == nil {
		t.Before = []*Trigger{}
	}
	if t.After == nil {
		t.After = []*Trigger{}
	}
	if timing == TimingAfter {
		t.After = append(t.After, tr)
	} else {
		t.Before = append(t.Before, tr)
	}
	return nil
}

// Triggers returns the triggers for one timing in registration order.
func (t *Table) Triggers(timing Timing) []*Trigger {
	if normaliseTiming(timing) == TimingAfter {
		return t.After
	}
	return t.Before
}

// AddTrigger appends a trigger to the list selected by its Timing field.
func (t *Table) AddTrigger(tr *Trigger) error {
	if tr == nil {
		return fmt.Errorf("netsec: nil trigger")
	}
	timing := normaliseTiming(tr.Timing)
	if tr.Timing == "" {
		// A trigger registered without timing defaults to BEFORE, which is the
		// conservative choice for row validation.
		tr.Timing = TimingBefore
	}
	return t.addTrigger(timing, tr)
}

// SortTriggers re-orders triggers by TriggerID. Registration order is the
// default; SortTriggers is provided for deterministic fixtures.
func (t *Table) SortTriggers() {
	sort.SliceStable(t.Before, func(i, j int) bool { return t.Before[i].ID < t.Before[j].ID })
	sort.SliceStable(t.After, func(i, j int) bool { return t.After[i].ID < t.After[j].ID })
}

// hasTable reports whether a declared writable-table set contains table.
// "*" and an empty declaration are interpreted as a wildcard by helper callers
// only; the engine treats an empty declaration as "no definer scope".
func hasTable(tables []string, table string) bool {
	for _, candidate := range tables {
		if strings.TrimSpace(candidate) == table {
			return true
		}
	}
	return false
}
