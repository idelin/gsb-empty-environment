package coordinator

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// collect feeds events into a running coordinator and gathers all outputs.
func collect(t *testing.T, c *Coordinator, events ...Event) []Output {
	t.Helper()
	in := make(chan Event)
	out := c.Run(context.Background(), in)
	go func() {
		for _, ev := range events {
			in <- ev
		}
		close(in)
	}()
	var outputs []Output
	for o := range out {
		outputs = append(outputs, o)
	}
	return outputs
}

func usersRegistry() *Registry {
	reg := NewRegistry()
	reg.Register("users",
		Column{Name: "id", Type: "INT"},
		Column{Name: "name", Type: "STRING"},
		Column{Name: "age", Type: "INT"},
	)
	return reg
}

// Acceptance boundary: DML_Old(3 cols) -> DDL(add col4) -> DML_New(4 cols).
// The coordinator must cut the metadata cache exactly at the DDL so the
// old row parses under v1 and the new row under v2 with zero misalignment.
func TestCoordinatorCutsSchemaAtDDL(t *testing.T) {
	c := NewCoordinator(usersRegistry(), 4)

	outputs := collect(t, c,
		DMLEvent{Table: "users", Raw: []string{"1", "alice", "30"}},
		DDLEvent{Table: "users", Kind: AddColumn, Column: Column{Name: "email", Type: "STRING"}},
		DMLEvent{Table: "users", Raw: []string{"2", "bob", "41", "bob@example.com"}},
	)

	if len(outputs) != 3 {
		t.Fatalf("expected 3 outputs (row, barrier, row), got %d: %#v", len(outputs), outputs)
	}

	oldRow, ok := outputs[0].(RowOutput)
	if !ok {
		t.Fatalf("output[0] = %T, want RowOutput", outputs[0])
	}
	if oldRow.Row.SchemaVersion != 1 {
		t.Errorf("old row schema version = %d, want 1", oldRow.Row.SchemaVersion)
	}
	if len(oldRow.Row.Fields) != 3 {
		t.Errorf("old row has %d fields, want 3", len(oldRow.Row.Fields))
	}
	if oldRow.Row.Fields["id"] != "1" || oldRow.Row.Fields["name"] != "alice" || oldRow.Row.Fields["age"] != "30" {
		t.Errorf("old row misaligned: %#v", oldRow.Row.Fields)
	}
	if _, leaked := oldRow.Row.Fields["email"]; leaked {
		t.Errorf("old row must not contain col4 'email': %#v", oldRow.Row.Fields)
	}

	barrier, ok := outputs[1].(BarrierOutput)
	if !ok {
		t.Fatalf("output[1] = %T, want BarrierOutput", outputs[1])
	}
	if barrier.Table != "users" || barrier.FromVersion != 1 || barrier.ToVersion != 2 {
		t.Errorf("barrier = %+v, want {users 1 -> 2}", barrier)
	}

	newRow, ok := outputs[2].(RowOutput)
	if !ok {
		t.Fatalf("output[2] = %T, want RowOutput", outputs[2])
	}
	if newRow.Row.SchemaVersion != 2 {
		t.Errorf("new row schema version = %d, want 2", newRow.Row.SchemaVersion)
	}
	if len(newRow.Row.Fields) != 4 {
		t.Errorf("new row has %d fields, want 4", len(newRow.Row.Fields))
	}
	if newRow.Row.Fields["id"] != "2" || newRow.Row.Fields["name"] != "bob" ||
		newRow.Row.Fields["age"] != "41" || newRow.Row.Fields["email"] != "bob@example.com" {
		t.Errorf("new row misaligned: %#v", newRow.Row.Fields)
	}
}

// The barrier must drain: with many in-flight old DML and multiple parse
// workers, every row before the barrier uses v1 and every row after uses v2.
func TestBarrierDrainsInFlightDML(t *testing.T) {
	const n = 200
	c := NewCoordinator(usersRegistry(), 8)

	events := make([]Event, 0, 2*n+1)
	for i := 0; i < n; i++ {
		events = append(events, DMLEvent{Table: "users", Raw: []string{fmt.Sprint(i), "old", "1"}})
	}
	events = append(events, DDLEvent{Table: "users", Kind: AddColumn, Column: Column{Name: "email", Type: "STRING"}})
	for i := 0; i < n; i++ {
		events = append(events, DMLEvent{Table: "users", Raw: []string{fmt.Sprint(i), "new", "2", "x@y.z"}})
	}

	outputs := collect(t, c, events...)
	if len(outputs) != 2*n+1 {
		t.Fatalf("expected %d outputs, got %d", 2*n+1, len(outputs))
	}

	barrierIdx := -1
	for i, o := range outputs {
		if _, ok := o.(BarrierOutput); ok {
			if barrierIdx != -1 {
				t.Fatalf("multiple barriers emitted")
			}
			barrierIdx = i
		}
	}
	if barrierIdx != n {
		t.Fatalf("barrier at index %d, want %d (after all old rows)", barrierIdx, n)
	}
	for i, o := range outputs {
		row, ok := o.(RowOutput)
		if !ok {
			continue
		}
		wantVersion := uint64(1)
		if i > barrierIdx {
			wantVersion = 2
		}
		if row.Row.SchemaVersion != wantVersion {
			t.Fatalf("row %d parsed with schema v%d, want v%d (misalignment across barrier)",
				i, row.Row.SchemaVersion, wantVersion)
		}
	}
}

// Sequential DDLs must produce a monotonically increasing version chain.
func TestMultipleSequentialDDL(t *testing.T) {
	c := NewCoordinator(usersRegistry(), 2)

	outputs := collect(t, c,
		DMLEvent{Table: "users", Raw: []string{"1", "a", "10"}},
		DDLEvent{Table: "users", Kind: AddColumn, Column: Column{Name: "email", Type: "STRING"}},
		DMLEvent{Table: "users", Raw: []string{"2", "b", "20", "b@x"}},
		DDLEvent{Table: "users", Kind: AddColumn, Column: Column{Name: "city", Type: "STRING"}},
		DMLEvent{Table: "users", Raw: []string{"3", "c", "30", "c@x", "sh"}},
	)

	wantVersions := []uint64{1, 2, 3}
	var gotVersions []uint64
	barriers := 0
	for _, o := range outputs {
		switch v := o.(type) {
		case RowOutput:
			gotVersions = append(gotVersions, v.Row.SchemaVersion)
		case BarrierOutput:
			barriers++
		}
	}
	if barriers != 2 {
		t.Fatalf("expected 2 barriers, got %d", barriers)
	}
	if len(gotVersions) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(gotVersions))
	}
	for i := range wantVersions {
		if gotVersions[i] != wantVersions[i] {
			t.Errorf("row %d version = %d, want %d", i, gotVersions[i], wantVersions[i])
		}
	}
	last := outputs[len(outputs)-1].(RowOutput)
	if last.Row.Fields["city"] != "sh" || len(last.Row.Fields) != 5 {
		t.Errorf("final row misaligned: %#v", last.Row.Fields)
	}
}

// A DML whose field count matches neither schema must surface as an
// explicit error, never as silently misaligned data.
func TestFieldCountMismatchIsAnError(t *testing.T) {
	c := NewCoordinator(usersRegistry(), 1)

	outputs := collect(t, c,
		DMLEvent{Table: "users", Raw: []string{"1", "alice"}}, // 2 fields vs 3 columns
	)
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(outputs))
	}
	errOut, ok := outputs[0].(ErrorOutput)
	if !ok {
		t.Fatalf("output = %T, want ErrorOutput", outputs[0])
	}
	var fcErr *FieldCountError
	if !errors.As(errOut.Err, &fcErr) {
		t.Fatalf("error = %T, want *FieldCountError", errOut.Err)
	}
	if fcErr.Got != 2 || fcErr.Want != 3 || fcErr.SchemaVersion != 1 {
		t.Errorf("FieldCountError = %+v, want {v1 got 2 want 3}", fcErr)
	}
}

// Events for unregistered tables are rejected without disturbing the stream.
func TestUnknownTable(t *testing.T) {
	c := NewCoordinator(usersRegistry(), 1)

	outputs := collect(t, c,
		DMLEvent{Table: "ghost", Raw: []string{"1"}},
		DDLEvent{Table: "ghost", Kind: AddColumn, Column: Column{Name: "x", Type: "INT"}},
		DMLEvent{Table: "users", Raw: []string{"1", "alice", "30"}},
	)
	if len(outputs) != 3 {
		t.Fatalf("expected 3 outputs, got %d", len(outputs))
	}
	for i := 0; i < 2; i++ {
		errOut, ok := outputs[i].(ErrorOutput)
		if !ok {
			t.Fatalf("output[%d] = %T, want ErrorOutput", i, outputs[i])
		}
		var utErr *UnknownTableError
		if !errors.As(errOut.Err, &utErr) {
			t.Errorf("output[%d] error = %T, want *UnknownTableError", i, errOut.Err)
		}
	}
	if row, ok := outputs[2].(RowOutput); !ok || row.Row.SchemaVersion != 1 {
		t.Errorf("stream did not recover after unknown-table errors: %#v", outputs[2])
	}
}

// Schema snapshots must be immutable: a DDL applied after dispatch must
// not mutate a snapshot already handed to an in-flight DML.
func TestSchemaSnapshotImmutability(t *testing.T) {
	reg := usersRegistry()
	snap, _ := reg.Get("users")
	if _, err := reg.Apply(DDLEvent{Table: "users", Kind: AddColumn, Column: Column{Name: "email", Type: "STRING"}}); err != nil {
		t.Fatal(err)
	}
	if len(snap.Columns) != 3 || snap.Version != 1 {
		t.Errorf("snapshot mutated by DDL: %+v", snap)
	}
	cur, _ := reg.Get("users")
	if len(cur.Columns) != 4 || cur.Version != 2 {
		t.Errorf("registry did not advance: %+v", cur)
	}
}
