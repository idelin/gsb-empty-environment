package memtx

import (
	"fmt"
	"reflect"
	"testing"
)

// The exhaustive test enumerates every transaction program of up to two
// operations over a tiny universe (3 primary keys x 3 unique values), then
// runs every ordered pair of programs under three schedules:
//
//	sequential:            A commits, then B begins and commits
//	concurrent A-then-B:   both begin on the same snapshot, A commits first
//	concurrent B-then-A:   both begin on the same snapshot, B commits first
//
// After every run it verifies:
//   - a failed commit left the main table and indexes completely untouched
//   - the final main table equals the replay of committed programs in
//     commit order (atomic, serial application)
//   - the unique indexes exactly match a rebuild from the main table
//   - a post-commit snapshot reads exactly the committed state

type progOp struct {
	del bool
	row Row
}

func allOps() []progOp {
	var ops []progOp
	for _, id := range []string{"r1", "r2", "r3"} {
		for _, email := range []string{"e1", "e2", "e3"} {
			ops = append(ops, progOp{row: Row{"id": id, "email": email}})
		}
		ops = append(ops, progOp{del: true, row: Row{"id": id}})
	}
	return ops
}

func allPrograms() [][]progOp {
	ops := allOps()
	progs := [][]progOp{{}} // the empty (read-only) transaction
	for _, a := range ops {
		progs = append(progs, []progOp{a})
		for _, b := range ops {
			progs = append(progs, []progOp{a, b})
		}
	}
	return progs
}

func seedRows() map[string]Row {
	return map[string]Row{
		"r1": {"id": "r1", "email": "e1"},
		"r2": {"id": "r2", "email": "e2"},
	}
}

func freshTable() *Table {
	tbl := NewTable("id", "email")
	for _, r := range seedRows() {
		tbl.Seed(r)
	}
	return tbl
}

func applyProg(tx *Txn, prog []progOp) error {
	for _, o := range prog {
		var err error
		if o.del {
			err = tx.Delete(o.row["id"])
		} else {
			err = tx.Put(o.row)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// replay applies committed programs to the seed state in commit order.
func replay(committed [][]progOp) map[string]Row {
	model := seedRows()
	for _, prog := range committed {
		for _, o := range prog {
			pk := o.row["id"]
			if o.del {
				delete(model, pk)
			} else {
				model[pk] = cloneRow(o.row)
			}
		}
	}
	return model
}

func classifyErr(err error) string {
	switch err.(type) {
	case *DuplicateKeyError:
		return "duplicate"
	}
	switch err {
	case nil:
		return "ok"
	case ErrWriteConflict:
		return "write-conflict"
	case ErrUniqueConflict:
		return "unique-conflict"
	}
	return "unexpected"
}

func TestExhaustiveTransactionPairs(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive pair enumeration is slow; run without -short")
	}
	progs := allPrograms()
	stats := make(map[string]int)

	check := func(tbl *Table, committed [][]progOp, ctx string) {
		t.Helper()
		final := tbl.View()
		if want := replay(committed); !reflect.DeepEqual(final, want) {
			t.Fatalf("%s: main table = %v, replay of committed = %v", ctx, final, want)
		}
		assertConsistent(t, tbl)
		// A fresh snapshot must read exactly the committed state.
		snap := tbl.Begin()
		if !reflect.DeepEqual(snap.snap, final) {
			t.Fatalf("%s: post-commit snapshot = %v, want %v", ctx, snap.snap, final)
		}
		for pk, want := range final {
			got, ok := snap.Get(pk)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: snapshot Get(%s) = %v,%v want %v", ctx, pk, got, ok, want)
			}
		}
		snap.Abort()
	}

	commit := func(tbl *Table, tx *Txn, prog []progOp, ctx string) bool {
		t.Helper()
		pre := tbl.View()
		err := applyProg(tx, prog)
		if err == nil {
			err = tx.Commit()
		}
		kind := classifyErr(err)
		stats[kind]++
		if kind == "unexpected" {
			t.Fatalf("%s: unexpected commit error: %v", ctx, err)
		}
		if kind != "ok" {
			if got := tbl.View(); !reflect.DeepEqual(got, pre) {
				t.Fatalf("%s: failed commit (%v) mutated table: %v -> %v", ctx, err, pre, got)
			}
			assertConsistent(t, tbl)
			return false
		}
		return true
	}

	for _, progA := range progs {
		for _, progB := range progs {
			ctx := fmt.Sprintf("pair A=%v B=%v", progA, progB)

			// Schedule 1: sequential, B begins after A's commit.
			tbl := freshTable()
			var committed [][]progOp
			if commit(tbl, tbl.Begin(), progA, ctx+" seq") {
				committed = append(committed, progA)
			}
			if commit(tbl, tbl.Begin(), progB, ctx+" seq") {
				committed = append(committed, progB)
			}
			check(tbl, committed, ctx+" seq")

			// Schedules 2 and 3: concurrent, both begin on one snapshot.
			for _, order := range [2][2]int{{0, 1}, {1, 0}} {
				tbl := freshTable()
				txA, txB := tbl.Begin(), tbl.Begin()
				pair := [2][]progOp{progA, progB}
				txns := [2]*Txn{txA, txB}
				committed = nil
				for _, idx := range order {
					if commit(tbl, txns[idx], pair[idx], ctx+" conc") {
						committed = append(committed, pair[idx])
					}
				}
				check(tbl, committed, ctx+" conc")
			}
		}
	}
	t.Logf("programs=%d pairs=%d commit results: %v", len(progs), len(progs)*len(progs), stats)
}
