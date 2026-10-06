package memtx

import (
	"reflect"
	"testing"
)

func seeded() *Table {
	tbl := NewTable("id", "email")
	tbl.Seed(
		Row{"id": "r1", "email": "e1"},
		Row{"id": "r2", "email": "e2"},
	)
	return tbl
}

// assertConsistent rebuilds the unique indexes from the main table and
// requires them to match the maintained ones exactly.
func assertConsistent(t *testing.T, tbl *Table) {
	t.Helper()
	tbl.mu.Lock()
	defer tbl.mu.Unlock()
	want := make(map[string]map[string]string)
	for _, f := range tbl.uniqFields {
		want[f] = make(map[string]string)
	}
	for pk, r := range tbl.rows {
		for _, f := range tbl.uniqFields {
			v, ok := r[f]
			if !ok {
				continue
			}
			if other, dup := want[f][v]; dup {
				t.Fatalf("main table has duplicate unique value %s=%q (pks %s, %s)", f, v, other, pk)
			}
			want[f][v] = pk
		}
	}
	if !reflect.DeepEqual(want, tbl.index) {
		t.Fatalf("index out of sync with main table:\n got: %v\nwant: %v", tbl.index, want)
	}
}

func mustCommit(t *testing.T, tx *Txn) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
}

func TestSwapUniqueKeysInOneTxn(t *testing.T) {
	tbl := seeded()
	tx := tbl.Begin()
	tx.Put(Row{"id": "r1", "email": "e2"})
	tx.Put(Row{"id": "r2", "email": "e1"})
	mustCommit(t, tx)

	want := map[string]Row{
		"r1": {"id": "r1", "email": "e2"},
		"r2": {"id": "r2", "email": "e1"},
	}
	if got := tbl.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	assertConsistent(t, tbl)
}

func TestDeleteAndReuseKeyInOneTxn(t *testing.T) {
	tbl := seeded()
	tx := tbl.Begin()
	tx.Delete("r1")
	tx.Put(Row{"id": "r9", "email": "e1"})
	mustCommit(t, tx)

	want := map[string]Row{
		"r2": {"id": "r2", "email": "e2"},
		"r9": {"id": "r9", "email": "e1"},
	}
	if got := tbl.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	assertConsistent(t, tbl)
}

func TestDuplicateWithinWriteSetRejected(t *testing.T) {
	tbl := seeded()
	before := tbl.View()
	tx := tbl.Begin()
	tx.Put(Row{"id": "r3", "email": "e3"})
	tx.Put(Row{"id": "r4", "email": "e3"})
	err := tx.Commit()
	if _, ok := err.(*DuplicateKeyError); !ok {
		t.Fatalf("err = %v, want DuplicateKeyError", err)
	}
	if got := tbl.View(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed commit mutated table: %v", got)
	}
	assertConsistent(t, tbl)
}

func TestDuplicateAgainstCommittedRejected(t *testing.T) {
	tbl := seeded()
	before := tbl.View()
	tx := tbl.Begin()
	tx.Put(Row{"id": "r3", "email": "e1"})
	err := tx.Commit()
	if _, ok := err.(*DuplicateKeyError); !ok {
		t.Fatalf("err = %v, want DuplicateKeyError", err)
	}
	if got := tbl.View(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed commit mutated table: %v", got)
	}
	assertConsistent(t, tbl)
}

func TestWriteWriteConflict(t *testing.T) {
	tbl := seeded()
	txA := tbl.Begin()
	txB := tbl.Begin()
	txA.Put(Row{"id": "r1", "email": "e9"})
	txB.Put(Row{"id": "r1", "email": "e8"})
	mustCommit(t, txA)
	if err := txB.Commit(); err != ErrWriteConflict {
		t.Fatalf("err = %v, want ErrWriteConflict", err)
	}
	assertConsistent(t, tbl)
}

func TestUniqueKeyRace(t *testing.T) {
	tbl := seeded()
	// Release e1 so both racers see it as free in their snapshots.
	setup := tbl.Begin()
	setup.Put(Row{"id": "r1", "email": "e7"})
	mustCommit(t, setup)

	txA := tbl.Begin()
	txB := tbl.Begin()
	txA.Put(Row{"id": "r3", "email": "e1"})
	txB.Put(Row{"id": "r4", "email": "e1"})
	mustCommit(t, txA)
	if err := txB.Commit(); err != ErrUniqueConflict {
		t.Fatalf("err = %v, want ErrUniqueConflict", err)
	}
	assertConsistent(t, tbl)
}

func TestConcurrentSwapOfSamePairConflicts(t *testing.T) {
	tbl := seeded()
	txA := tbl.Begin()
	txB := tbl.Begin()
	txA.Put(Row{"id": "r1", "email": "e2"})
	txA.Put(Row{"id": "r2", "email": "e1"})
	txB.Put(Row{"id": "r1", "email": "e2"})
	txB.Put(Row{"id": "r2", "email": "e1"})
	mustCommit(t, txA)
	if err := txB.Commit(); err != ErrWriteConflict {
		t.Fatalf("err = %v, want ErrWriteConflict", err)
	}
	assertConsistent(t, tbl)
}

func TestSnapshotReadsAreFixed(t *testing.T) {
	tbl := seeded()
	tx := tbl.Begin()
	other := tbl.Begin()
	other.Put(Row{"id": "r1", "email": "e9"})
	other.Delete("r2")
	mustCommit(t, other)

	got, ok := tx.Get("r1")
	if !ok || got["email"] != "e1" {
		t.Fatalf("snapshot read r1 = %v, %v; want email e1", got, ok)
	}
	if _, ok := tx.Get("r2"); !ok {
		t.Fatalf("snapshot read r2 missing; want still present")
	}
	tx.Abort()
}

func TestReadYourOwnWrites(t *testing.T) {
	tbl := seeded()
	tx := tbl.Begin()
	tx.Put(Row{"id": "r1", "email": "e5"})
	tx.Delete("r2")
	got, _ := tx.Get("r1")
	if got["email"] != "e5" {
		t.Fatalf("own write not visible: %v", got)
	}
	if _, ok := tx.Get("r2"); ok {
		t.Fatalf("own delete not visible")
	}
	tx.Abort()
}

func TestFailedCommitLeavesNoPartialIndex(t *testing.T) {
	tbl := seeded()
	before := tbl.View()
	tx := tbl.Begin()
	// First op alone would be a legal index change; the second makes the
	// commit fail, and nothing may leak into the index.
	tx.Put(Row{"id": "r3", "email": "e3"})
	tx.Put(Row{"id": "r4", "email": "e1"})
	if err := tx.Commit(); err == nil {
		t.Fatalf("commit unexpectedly succeeded")
	}
	if got := tbl.View(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed commit mutated table: got %v want %v", got, before)
	}
	assertConsistent(t, tbl)
	// A later transaction must still be able to claim e3.
	tx2 := tbl.Begin()
	tx2.Put(Row{"id": "r3", "email": "e3"})
	mustCommit(t, tx2)
	assertConsistent(t, tbl)
}
