package memtx

import (
	"errors"
	"reflect"
	"testing"
)

func mustDump(t *testing.T, tbl *Table) (map[int]Row, map[string]int) {
	t.Helper()
	rows, idx := tbl.Dump()
	wantIdx := make(map[string]int, len(rows))
	for id, r := range rows {
		if prev, dup := wantIdx[r.Email]; dup {
			t.Fatalf("duplicate email %q: rows %d and %d", r.Email, prev, id)
		}
		wantIdx[r.Email] = id
	}
	if !reflect.DeepEqual(idx, wantIdx) {
		t.Fatalf("index %v inconsistent with rows %v", idx, rows)
	}
	return rows, idx
}

func TestSwapUniqueKeys(t *testing.T) {
	tbl := NewTable(Row{1, "a"}, Row{2, "b"})
	tx := tbl.Begin()
	tx.Put(Row{1, "b"})
	tx.Put(Row{2, "a"})
	if err := tx.Commit(); err != nil {
		t.Fatalf("swap commit: %v", err)
	}
	rows, _ := mustDump(t, tbl)
	want := map[int]Row{1: {1, "b"}, 2: {2, "a"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
}

func TestDeleteAndReuseKey(t *testing.T) {
	tbl := NewTable(Row{1, "a"}, Row{2, "b"})
	tx := tbl.Begin()
	tx.Delete(1)
	tx.Put(Row{3, "a"})
	if err := tx.Commit(); err != nil {
		t.Fatalf("delete+reuse commit: %v", err)
	}
	rows, _ := mustDump(t, tbl)
	want := map[int]Row{2: {2, "b"}, 3: {3, "a"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
}

func TestWriteWriteConflict(t *testing.T) {
	tbl := NewTable(Row{1, "a"}, Row{2, "b"})
	tx1 := tbl.Begin()
	tx2 := tbl.Begin()
	tx1.Put(Row{1, "x"})
	tx2.Put(Row{1, "y"})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}
	if err := tx2.Commit(); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("tx2 commit = %v, want ErrWriteConflict", err)
	}
	rows, _ := mustDump(t, tbl)
	want := map[int]Row{1: {1, "x"}, 2: {2, "b"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
}

func TestUniqueKeyContention(t *testing.T) {
	tbl := NewTable(Row{1, "a"}, Row{2, "b"})
	tx1 := tbl.Begin()
	tx2 := tbl.Begin()
	tx1.Put(Row{3, "c"})
	tx2.Put(Row{4, "c"})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}
	if err := tx2.Commit(); !errors.Is(err, ErrUniqueConflict) {
		t.Fatalf("tx2 commit = %v, want ErrUniqueConflict", err)
	}
	rows, _ := mustDump(t, tbl)
	want := map[int]Row{1: {1, "a"}, 2: {2, "b"}, 3: {3, "c"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
}

func TestIntraTxnDuplicateEmail(t *testing.T) {
	tbl := NewTable(Row{1, "a"})
	tx := tbl.Begin()
	tx.Put(Row{2, "x"})
	tx.Put(Row{3, "x"})
	if err := tx.Commit(); !errors.Is(err, ErrUniqueConflict) {
		t.Fatalf("commit = %v, want ErrUniqueConflict", err)
	}
	rows, _ := mustDump(t, tbl)
	if !reflect.DeepEqual(rows, map[int]Row{1: {1, "a"}}) {
		t.Fatalf("rows = %v, want unchanged", rows)
	}
}

func TestSnapshotReadsAreFixed(t *testing.T) {
	tbl := NewTable(Row{1, "a"}, Row{2, "b"})
	tx := tbl.Begin()
	disjoint := tbl.Begin()
	other := tbl.Begin()
	other.Put(Row{1, "z"})
	other.Delete(2)
	other.Put(Row{3, "c"})
	if err := other.Commit(); err != nil {
		t.Fatalf("other commit: %v", err)
	}
	if r, ok := tx.Get(1); !ok || r.Email != "a" {
		t.Fatalf("Get(1) = %v,%v, want {1 a},true", r, ok)
	}
	if r, ok := tx.Get(2); !ok || r.Email != "b" {
		t.Fatalf("Get(2) = %v,%v, want {2 b},true", r, ok)
	}
	if _, ok := tx.Get(3); ok {
		t.Fatal("Get(3) found row committed after snapshot")
	}
	// Own writes overlay the snapshot.
	tx.Put(Row{1, "mine"})
	if r, _ := tx.Get(1); r.Email != "mine" {
		t.Fatalf("Get(1) after own Put = %v", r)
	}
	// A disjoint write set still commits under snapshot isolation.
	disjoint.Put(Row{4, "d"})
	if err := disjoint.Commit(); err != nil {
		t.Fatalf("disjoint commit: %v", err)
	}
	// Touching a row modified after the snapshot aborts the commit.
	if err := tx.Commit(); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("tx commit = %v, want ErrWriteConflict", err)
	}
	rows, _ := mustDump(t, tbl)
	want := map[int]Row{1: {1, "z"}, 3: {3, "c"}, 4: {4, "d"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %v, want %v", rows, want)
	}
}

func TestFailedCommitLeavesNoPartialChanges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, tbl *Table) (fail *Txn)
	}{
		{"write-write", func(t *testing.T, tbl *Table) *Txn {
			tx := tbl.Begin()
			tx.Put(Row{1, "x"})
			tx.Put(Row{3, "c"}) // would also touch the index if applied
			winner := tbl.Begin()
			winner.Delete(1)
			if err := winner.Commit(); err != nil {
				t.Fatal(err)
			}
			return tx
		}},
		{"unique", func(t *testing.T, tbl *Table) *Txn {
			tx := tbl.Begin()
			tx.Delete(2) // would remove index entry "b" if applied
			tx.Put(Row{3, "c"})
			winner := tbl.Begin()
			winner.Put(Row{4, "c"})
			if err := winner.Commit(); err != nil {
				t.Fatal(err)
			}
			return tx
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tbl := NewTable(Row{1, "a"}, Row{2, "b"})
			tx := tc.setup(t, tbl)
			beforeRows, beforeIdx := tbl.Dump()
			if err := tx.Commit(); err == nil {
				t.Fatal("commit unexpectedly succeeded")
			}
			afterRows, afterIdx := tbl.Dump()
			if !reflect.DeepEqual(afterRows, beforeRows) {
				t.Fatalf("rows changed by failed commit: %v -> %v", beforeRows, afterRows)
			}
			if !reflect.DeepEqual(afterIdx, beforeIdx) {
				t.Fatalf("index changed by failed commit: %v -> %v", beforeIdx, afterIdx)
			}
		})
	}
}
