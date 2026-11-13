package netsec

import (
	"errors"
	"reflect"
	"testing"
)

const (
	colTenant  ColumnID = 1
	colID      ColumnID = 2
	colPayload ColumnID = 3
	colToast   ColumnID = 4
	colExtra   ColumnID = 5

	testRelationID RelationID = 42
)

func baseRelation(version RelationVersion) Relation {
	return Relation{
		ID:      testRelationID,
		Version: version,
		Columns: []Column{
			{ID: colTenant, Name: "tenant", Type: "text"},
			{ID: colID, Name: "id", Type: "int"},
			{ID: colPayload, Name: "payload", Type: "text"},
			{ID: colToast, Name: "toast_value", Type: "text", AllowUnchanged: true},
		},
		PrimaryKey: []ColumnID{colID},
	}
}

func testRow(tenant string, id int, payload, toast string) Row {
	return Row{
		colTenant:  tenant,
		colID:      id,
		colPayload: payload,
		colToast:   toast,
	}
}

func allowTenantA(_ Relation, row Row) bool {
	return row[colTenant] == "A"
}

type outputAudit struct {
	t               *testing.T
	predicate       Predicate
	primaryKey      []ColumnID
	expectedColumns int
	changes         []Change
}

func (a *outputAudit) emit(change Change) {
	a.t.Helper()
	switch change.Kind {
	case DeleteChange:
		if change.Row != nil {
			a.t.Errorf("Delete output leaked a full row: %#v", change.Row)
		}
		if change.Key != nil {
			a.t.Errorf("Delete output unexpectedly carried a new key: %#v", change.Key)
		}
		if len(change.OldKey) == 0 {
			a.t.Errorf("Delete output has no old key")
		}
		a.assertKeyColumns(change.OldKey)
	case InsertChange:
		if change.Row == nil || !a.predicate(Relation{}, change.Row) {
			a.t.Errorf("Insert output contains a complete row rejected by the predicate: %#v", change.Row)
		}
		if change.OldKey != nil {
			a.t.Errorf("Insert output unexpectedly carried an old key: %#v", change.OldKey)
		}
		a.assertCompleteRow(change.Key, change.Row)
	case UpdateChange:
		if change.Row == nil || !a.predicate(Relation{}, change.Row) {
			a.t.Errorf("Update output contains a complete row rejected by the predicate: %#v", change.Row)
		}
		if len(change.OldKey) == 0 {
			a.t.Errorf("Update output has no old key")
		}
		a.assertKeyColumns(change.OldKey)
		a.assertCompleteRow(change.Key, change.Row)
	default:
		a.t.Errorf("unexpected change kind %q", change.Kind)
	}
	a.changes = append(a.changes, change)
}

func (a *outputAudit) assertKeyColumns(key Key) {
	a.t.Helper()
	if len(key) != len(a.primaryKey) {
		a.t.Errorf("output key has %d columns, want %d: %#v", len(key), len(a.primaryKey), key)
		return
	}
	for _, id := range a.primaryKey {
		if _, exists := key[id]; !exists {
			a.t.Errorf("output key is missing primary-key column %d: %#v", id, key)
		}
	}
	for id := range key {
		found := false
		for _, primaryID := range a.primaryKey {
			if id == primaryID {
				found = true
				break
			}
		}
		if !found {
			a.t.Errorf("output key contains non-key column %d: %#v", id, key)
		}
	}
}

func (a *outputAudit) assertCompleteRow(key Key, row Row) {
	a.t.Helper()
	a.assertKeyColumns(key)
	if len(row) != a.expectedColumns {
		a.t.Errorf("output row has %d columns, want %d: %#v", len(row), a.expectedColumns, row)
	}
	for id, value := range key {
		if !reflect.DeepEqual(row[id], value) {
			a.t.Errorf("output key column %d=%v disagrees with row value %v", id, value, row[id])
		}
	}
}

func newHarness(t *testing.T) (*Filter, *outputAudit, Relation) {
	t.Helper()
	audit := &outputAudit{
		t:               t,
		predicate:       allowTenantA,
		primaryKey:      []ColumnID{colID},
		expectedColumns: 4,
	}
	filter, err := New(allowTenantA, audit.emit)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	relation := baseRelation(1)
	if err := filter.Apply(RelationChange{Relation: relation}); err != nil {
		t.Fatalf("install relation: %v", err)
	}
	return filter, audit, relation
}

func mustApply(t *testing.T, filter *Filter, event Event) {
	t.Helper()
	if err := filter.Apply(event); err != nil {
		t.Fatalf("Apply(%T): %v", event, err)
	}
}

func assertNeedsFetch(t *testing.T, err error, missing ...ColumnID) {
	t.Helper()
	if !errors.Is(err, ErrNeedsFetch) {
		t.Fatalf("error = %v, want ErrNeedsFetch", err)
	}
	var fetch *NeedsFetchError
	if !errors.As(err, &fetch) {
		t.Fatalf("error %v has no NeedsFetchError detail", err)
	}
	got := append([]ColumnID(nil), fetch.Missing...)
	want := append([]ColumnID(nil), missing...)
	sortColumnIDs(got)
	sortColumnIDs(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missing columns = %v, want %v", got, want)
	}
}

func TestCrossTenantUpdateEmitsDeleteWithOnlyOldKey(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 1})
	mustApply(t, filter, Insert{XID: 1, RelationID: testRelationID,
		Row: testRow("A", 1, "old", "large-toast-A")})
	mustApply(t, filter, Commit{XID: 1})
	if len(audit.changes) != 1 || audit.changes[0].Kind != InsertChange {
		t.Fatalf("after insert changes = %#v", audit.changes)
	}

	mustApply(t, filter, Begin{XID: 2})
	mustApply(t, filter, Update{
		XID:        2,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "B",
			colID:      1,
			colPayload: "new-payload-B",
			colToast:   Unchanged,
		},
	})
	if len(audit.changes) != 1 {
		t.Fatalf("output was published before commit: %#v", audit.changes)
	}
	mustApply(t, filter, Commit{XID: 2})

	if len(audit.changes) != 2 {
		t.Fatalf("changes = %#v, want insert+delete", audit.changes)
	}
	change := audit.changes[1]
	if change.Kind != DeleteChange {
		t.Fatalf("cross-tenant update kind = %q, want delete", change.Kind)
	}
	if !reflect.DeepEqual(change.OldKey, Key{colID: 1}) {
		t.Fatalf("delete old key = %#v", change.OldKey)
	}
	if change.Row != nil || change.Key != nil {
		t.Fatalf("delete leaked row/new key: %#v", change)
	}

	// The B row was not cached. Moving B back to A with a full old row and a
	// full new row must be a false->true transition, emitted as Insert.
	mustApply(t, filter, Begin{XID: 3})
	mustApply(t, filter, Update{
		XID:        3,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   testRow("B", 1, "new-payload-B", "toast-B"),
		NewTuple:   testRow("A", 1, "back-to-A", "toast-A"),
	})
	mustApply(t, filter, Commit{XID: 3})
	if len(audit.changes) != 3 || audit.changes[2].Kind != InsertChange {
		t.Fatalf("false->true changes = %#v, want insert", audit.changes)
	}

	// Delete can reconstruct from the cache and must emit only the old key.
	mustApply(t, filter, Begin{XID: 4})
	mustApply(t, filter, Delete{
		XID:        4,
		RelationID: testRelationID,
		OldTuple:   Row{colID: 1},
	})
	mustApply(t, filter, Commit{XID: 4})
	if len(audit.changes) != 4 || audit.changes[3].Kind != DeleteChange {
		t.Fatalf("delete changes = %#v", audit.changes)
	}

	// The committed cache must have dropped the deleted authenticated row.
	mustApply(t, filter, Begin{XID: 5})
	err := filter.Apply(Delete{
		XID:        5,
		RelationID: testRelationID,
		OldTuple:   Row{colID: 1},
	})
	assertNeedsFetch(t, err, colTenant, colPayload, colToast)
	mustApply(t, filter, Abort{XID: 5})
}

func TestPrimaryKeyChangeRequiresFetchForUnchangedValues(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 10})
	mustApply(t, filter, Insert{XID: 10, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-1")})
	mustApply(t, filter, Commit{XID: 10})

	mustApply(t, filter, Begin{XID: 11})
	keyChange := Update{
		XID:        11,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      2,
			colPayload: "p2",
			colToast:   Unchanged,
		},
	}
	err := filter.Apply(keyChange)
	assertNeedsFetch(t, err, colToast)
	if len(audit.changes) != 1 {
		t.Fatalf("failed event changed buffered output: %#v", audit.changes)
	}

	// Retrying with the missing value must not have left partial state behind.
	keyChange.NewTuple[colToast] = "toast-2"
	mustApply(t, filter, keyChange)
	mustApply(t, filter, Commit{XID: 11})

	if len(audit.changes) != 2 {
		t.Fatalf("changes = %#v", audit.changes)
	}
	change := audit.changes[1]
	if change.Kind != UpdateChange {
		t.Fatalf("key change kind = %q, want update", change.Kind)
	}
	if !reflect.DeepEqual(change.OldKey, Key{colID: 1}) || !reflect.DeepEqual(change.Key, Key{colID: 2}) {
		t.Fatalf("key change old/new key = %#v/%#v", change.OldKey, change.Key)
	}
	if change.Row[colToast] != "toast-2" {
		t.Fatalf("key change row = %#v", change.Row)
	}

	// The migrated key is now cached for unchanged TOAST reconstruction.
	mustApply(t, filter, Begin{XID: 12})
	mustApply(t, filter, Update{
		XID:        12,
		RelationID: testRelationID,
		OldKey:     Key{colID: 2},
		OldTuple:   Row{colID: 2},
		NewTuple: Row{
			colTenant:  "A",
			colID:      2,
			colPayload: "p3",
			colToast:   Unchanged,
		},
	})
	mustApply(t, filter, Commit{XID: 12})
	if len(audit.changes) != 3 || audit.changes[2].Kind != UpdateChange {
		t.Fatalf("toast update changes = %#v", audit.changes)
	}
	if audit.changes[2].Row[colToast] != "toast-2" {
		t.Fatalf("unchanged TOAST was not reconstructed from the new key cache: %#v", audit.changes[2].Row)
	}

	// The old-key cache entry must no longer be usable.
	mustApply(t, filter, Begin{XID: 13})
	err = filter.Apply(Update{
		XID:        13,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "stale",
			colToast:   Unchanged,
		},
	})
	assertNeedsFetch(t, err, colTenant, colPayload, colToast)
	mustApply(t, filter, Abort{XID: 13})
}

func TestUnchangedCannotBorrowAnotherTenantsOrKeysRow(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 20})
	mustApply(t, filter, Insert{XID: 20, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-A")})
	mustApply(t, filter, Commit{XID: 20})

	// B is filtered out and therefore must not enter the authenticated cache.
	mustApply(t, filter, Begin{XID: 21})
	mustApply(t, filter, Insert{XID: 21, RelationID: testRelationID,
		Row: testRow("B", 2, "p2", "toast-B")})
	mustApply(t, filter, Commit{XID: 21})
	if len(audit.changes) != 1 {
		t.Fatalf("filtered insert was emitted: %#v", audit.changes)
	}

	mustApply(t, filter, Begin{XID: 22})
	err := filter.Apply(Update{
		XID:        22,
		RelationID: testRelationID,
		OldKey:     Key{colID: 2},
		OldTuple:   testRow("B", 2, "p2", "toast-B"),
		NewTuple: Row{
			colTenant:  "A",
			colID:      2,
			colPayload: "p2b",
			colToast:   Unchanged,
		},
	})
	assertNeedsFetch(t, err, colToast)
	mustApply(t, filter, Abort{XID: 22})
}

func TestRelationMigrationUsesColumnIDsNotPositions(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 30})
	mustApply(t, filter, Insert{XID: 30, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-1")})
	mustApply(t, filter, Commit{XID: 30})

	reordered := baseRelation(2)
	reordered.Columns = []Column{
		{ID: colToast, Name: "toast_value", Type: "text", AllowUnchanged: true},
		{ID: colID, Name: "id", Type: "int"},
		{ID: colTenant, Name: "tenant", Type: "text"},
		{ID: colPayload, Name: "payload", Type: "text"},
	}
	mustApply(t, filter, RelationChange{Relation: reordered})

	mustApply(t, filter, Begin{XID: 31})
	mustApply(t, filter, Update{
		XID:        31,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "p2",
			colToast:   Unchanged,
		},
	})
	mustApply(t, filter, Commit{XID: 31})
	if len(audit.changes) != 2 || audit.changes[1].Kind != UpdateChange {
		t.Fatalf("migration update changes = %#v", audit.changes)
	}
	if audit.changes[1].Row[colTenant] != "A" || audit.changes[1].Row[colToast] != "toast-1" {
		t.Fatalf("migration mapped columns by position: %#v", audit.changes[1].Row)
	}

	deleted := baseRelation(3)
	deleted.Columns = deleted.Columns[:3]
	err := filter.Apply(RelationChange{Relation: deleted})
	if !errors.Is(err, ErrColumnDeleted) {
		t.Fatalf("deleted column error = %v, want ErrColumnDeleted", err)
	}

	reused := baseRelation(3)
	reused.Columns[2].Type = "bytea"
	err = filter.Apply(RelationChange{Relation: reused})
	if !errors.Is(err, ErrColumnReused) {
		t.Fatalf("reused column error = %v, want ErrColumnReused", err)
	}

	// Manual resync is the explicit escape hatch and starts a fresh history.
	manual := baseRelation(4)
	manual.Columns[3].Type = "bytea"
	if err := filter.ResyncRelation(manual); err != nil {
		t.Fatalf("ResyncRelation: %v", err)
	}
}

func TestAddedColumnIsNotFabricatedDuringMigration(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 35})
	mustApply(t, filter, Insert{XID: 35, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-1")})
	mustApply(t, filter, Commit{XID: 35})

	withExtra := baseRelation(2)
	withExtra.Columns = append(withExtra.Columns,
		Column{ID: colExtra, Name: "extra", Type: "text"})
	mustApply(t, filter, RelationChange{Relation: withExtra})
	audit.expectedColumns = 5

	// The migrated cache contains the old columns by ID, but cannot invent the
	// value of the newly added column.
	mustApply(t, filter, Begin{XID: 36})
	err := filter.Apply(Update{
		XID:        36,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "p2",
			colToast:   Unchanged,
			colExtra:   "extra-value",
		},
	})
	assertNeedsFetch(t, err, colExtra)

	// Supplying the old-version-2 row lets the unchanged TOAST come from the
	// migrated cache and emits a complete new row.
	mustApply(t, filter, Update{
		XID:        36,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "p1",
			colToast:   "toast-1",
			colExtra:   "extra-value",
		},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "p2",
			colToast:   Unchanged,
			colExtra:   "extra-value",
		},
	})
	mustApply(t, filter, Commit{XID: 36})
	if len(audit.changes) != 2 || audit.changes[1].Kind != UpdateChange {
		t.Fatalf("added-column update changes = %#v", audit.changes)
	}
	if audit.changes[1].Row[colToast] != "toast-1" || audit.changes[1].Row[colExtra] != "extra-value" {
		t.Fatalf("added-column output = %#v", audit.changes[1].Row)
	}
}

func TestStreamedTransactionsInterleaveWithPrivateCachesAndAbort(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 40})
	mustApply(t, filter, Insert{XID: 40, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-1")})

	// A second transaction cannot see transaction 40's private cache.
	mustApply(t, filter, Begin{XID: 41})
	err := filter.Apply(Update{
		XID:        41,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "cross-tx",
			colToast:   Unchanged,
		},
	})
	assertNeedsFetch(t, err, colTenant, colPayload, colToast)

	mustApply(t, filter, Begin{XID: 42})
	mustApply(t, filter, Insert{XID: 42, RelationID: testRelationID,
		Row: testRow("A", 2, "p2", "toast-2")})
	mustApply(t, filter, Abort{XID: 42})

	// Transaction 40 can use its own earlier Insert through its private cache.
	mustApply(t, filter, Update{
		XID:        40,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "p1-updated",
			colToast:   Unchanged,
		},
	})
	if len(audit.changes) != 0 {
		t.Fatalf("output was emitted before any commit: %#v", audit.changes)
	}

	mustApply(t, filter, Abort{XID: 41})
	mustApply(t, filter, Commit{XID: 40})
	if len(audit.changes) != 2 {
		t.Fatalf("committed changes = %#v", audit.changes)
	}
	if audit.changes[0].Kind != InsertChange || audit.changes[1].Kind != UpdateChange {
		t.Fatalf("commit order = %#v", audit.changes)
	}
	if audit.changes[1].Row[colPayload] != "p1-updated" || audit.changes[1].Row[colToast] != "toast-1" {
		t.Fatalf("private update cache was not used: %#v", audit.changes[1].Row)
	}

	// The aborted transaction's row must not be in the committed cache.
	mustApply(t, filter, Begin{XID: 43})
	err = filter.Apply(Delete{
		XID:        43,
		RelationID: testRelationID,
		OldTuple:   Row{colID: 2},
	})
	assertNeedsFetch(t, err, colTenant, colPayload, colToast)
	mustApply(t, filter, Abort{XID: 43})
}

func TestFourUpdateBranchesAndFilteredInsertDelete(t *testing.T) {
	filter, audit, _ := newHarness(t)

	// filtered insert: no output, no cache
	mustApply(t, filter, Begin{XID: 50})
	mustApply(t, filter, Insert{XID: 50, RelationID: testRelationID,
		Row: testRow("B", 1, "b1", "toast-b1")})
	mustApply(t, filter, Commit{XID: 50})
	if len(audit.changes) != 0 {
		t.Fatalf("filtered insert emitted: %#v", audit.changes)
	}

	// false -> false: no output
	mustApply(t, filter, Begin{XID: 51})
	mustApply(t, filter, Update{
		XID:        51,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   testRow("B", 1, "b1", "toast-b1"),
		NewTuple:   testRow("B", 1, "b2", "toast-b2"),
	})
	mustApply(t, filter, Commit{XID: 51})
	if len(audit.changes) != 0 {
		t.Fatalf("false->false emitted: %#v", audit.changes)
	}

	// false -> true: Insert with complete new row
	mustApply(t, filter, Begin{XID: 52})
	mustApply(t, filter, Update{
		XID:        52,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   testRow("B", 1, "b2", "toast-b2"),
		NewTuple:   testRow("A", 1, "a1", "toast-a1"),
	})
	mustApply(t, filter, Commit{XID: 52})
	if len(audit.changes) != 1 || audit.changes[0].Kind != InsertChange {
		t.Fatalf("false->true changes = %#v", audit.changes)
	}

	// true -> true: Update with old key and complete new row
	mustApply(t, filter, Begin{XID: 53})
	mustApply(t, filter, Update{
		XID:        53,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "a2",
			colToast:   Unchanged,
		},
	})
	mustApply(t, filter, Commit{XID: 53})
	if len(audit.changes) != 2 || audit.changes[1].Kind != UpdateChange {
		t.Fatalf("true->true changes = %#v", audit.changes)
	}

	// true -> false: Delete with only the old key
	mustApply(t, filter, Begin{XID: 54})
	mustApply(t, filter, Update{
		XID:        54,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple:   testRow("B", 1, "b3", "toast-b3"),
	})
	mustApply(t, filter, Commit{XID: 54})
	if len(audit.changes) != 3 || audit.changes[2].Kind != DeleteChange {
		t.Fatalf("true->false changes = %#v", audit.changes)
	}

	// filtered delete: no output
	mustApply(t, filter, Begin{XID: 55})
	mustApply(t, filter, Delete{
		XID:        55,
		RelationID: testRelationID,
		OldTuple:   testRow("B", 1, "b3", "toast-b3"),
	})
	mustApply(t, filter, Commit{XID: 55})
	if len(audit.changes) != 3 {
		t.Fatalf("filtered delete emitted: %#v", audit.changes)
	}
}

func TestNewTupleMustUseExplicitUnchangedMarker(t *testing.T) {
	filter, audit, _ := newHarness(t)

	mustApply(t, filter, Begin{XID: 60})
	mustApply(t, filter, Insert{XID: 60, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-1")})
	mustApply(t, filter, Commit{XID: 60})

	mustApply(t, filter, Begin{XID: 61})
	err := filter.Apply(Update{
		XID:        61,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: "p2",
			// colToast is absent, not marked UNCHANGED.
		},
	})
	assertNeedsFetch(t, err, colToast)
	mustApply(t, filter, Abort{XID: 61})
	if len(audit.changes) != 1 {
		t.Fatalf("incomplete update emitted: %#v", audit.changes)
	}
}

func TestInvalidUnchangedAndRelationReplacement(t *testing.T) {
	filter, _, relation := newHarness(t)

	mustApply(t, filter, Begin{XID: 70})
	mustApply(t, filter, Insert{XID: 70, RelationID: testRelationID,
		Row: testRow("A", 1, "p1", "toast-1")})
	mustApply(t, filter, Commit{XID: 70})

	mustApply(t, filter, Begin{XID: 71})
	err := filter.Apply(Update{
		XID:        71,
		RelationID: testRelationID,
		OldKey:     Key{colID: 1},
		OldTuple:   Row{colID: 1},
		NewTuple: Row{
			colTenant:  "A",
			colID:      1,
			colPayload: Unchanged,
			colToast:   Unchanged,
		},
	})
	if !errors.Is(err, ErrInvalidTuple) {
		t.Fatalf("disallowed UNCHANGED error = %v, want ErrInvalidTuple", err)
	}
	mustApply(t, filter, Abort{XID: 71})

	changedSameVersion := relation
	changedSameVersion.Columns[2].Type = "bytea"
	err = filter.Apply(RelationChange{Relation: changedSameVersion})
	if !errors.Is(err, ErrRelationVersionReuse) {
		t.Fatalf("same-version replacement error = %v", err)
	}
}
