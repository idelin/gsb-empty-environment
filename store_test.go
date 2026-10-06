package gcrt

import (
	"bytes"
	"math/big"
	"testing"
)

// fullMerge is the independent oracle: a from-scratch generalized merge of
// the given original constraints, in ID order.
func fullMerge(t *testing.T, cons []Constraint) Solution {
	t.Helper()
	merged := Normalize(big.NewInt(0), big.NewInt(1))
	for _, c := range cons {
		next, ok := Merge(merged, Normalize(c.Res, c.Mod))
		if !ok {
			return Solution{OK: false}
		}
		merged = next
	}
	return Solution{OK: true, Res: merged.Res, Period: merged.Mod}
}

func requireMatchesFullMerge(t *testing.T, st *Store, version int) {
	t.Helper()
	sol, err := st.Solve(version)
	if err != nil {
		t.Fatal(err)
	}
	cons, err := st.Constraints(version)
	if err != nil {
		t.Fatal(err)
	}
	want := fullMerge(t, cons)
	if sol.OK != want.OK {
		t.Fatalf("version %d: OK=%v, full merge says %v", version, sol.OK, want.OK)
	}
	if sol.OK && (sol.Res.Cmp(want.Res) != 0 || sol.Period.Cmp(want.Period) != 0) {
		t.Fatalf("version %d: x≡%s mod %s, full merge says x≡%s mod %s",
			version, sol.Res, sol.Period, want.Res, want.Period)
	}
}

// buildHistory creates a store whose versions mix additions, removals and
// an inconsistent snapshot.
//
//	v1: +A x≡1 mod 2, +B x≡1 mod 4, +C x≡2 mod 3   -> x≡5 mod 12
//	v2: -B                                          -> x≡5 mod 6
//	v3: +D x≡2 mod 6                                -> infeasible (A vs D)
//	v4: -A                                          -> x≡2 mod 6
func buildHistory(t *testing.T) (*Store, [4]uint64) {
	t.Helper()
	st := NewStore()
	tx := st.Begin()
	a, _ := tx.Add(big.NewInt(2), big.NewInt(1))
	b, _ := tx.Add(big.NewInt(4), big.NewInt(1))
	c, _ := tx.Add(big.NewInt(3), big.NewInt(2))
	v1, err := tx.Commit()
	if err != nil || v1 != 1 {
		t.Fatalf("commit v1: %v %d", err, v1)
	}
	tx = st.Begin()
	if err := tx.Remove(b); err != nil {
		t.Fatal(err)
	}
	if v, _ := tx.Commit(); v != 2 {
		t.Fatalf("commit v2: %d", v)
	}
	tx = st.Begin()
	d, _ := tx.Add(big.NewInt(6), big.NewInt(2))
	if v, _ := tx.Commit(); v != 3 {
		t.Fatalf("commit v3: %d", v)
	}
	tx = st.Begin()
	if err := tx.Remove(a); err != nil {
		t.Fatal(err)
	}
	if v, _ := tx.Commit(); v != 4 {
		t.Fatalf("commit v4: %d", v)
	}
	return st, [4]uint64{a, b, c, d}
}

func TestSnapshotsAndRemoval(t *testing.T) {
	st, ids := buildHistory(t)

	// Every committed snapshot matches a full re-merge of its originals.
	for v := 0; v <= st.Latest(); v++ {
		requireMatchesFullMerge(t, st, v)
	}

	// Old snapshots stay queryable with their original answers.
	sol, _ := st.Solve(1)
	if !sol.OK || sol.Res.Cmp(big.NewInt(5)) != 0 || sol.Period.Cmp(big.NewInt(12)) != 0 {
		t.Fatalf("v1: got %+v, want x≡5 mod 12", sol)
	}
	sol, _ = st.Solve(2)
	if !sol.OK || sol.Res.Cmp(big.NewInt(5)) != 0 || sol.Period.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("v2 (after removing B): got %+v, want x≡5 mod 6", sol)
	}

	// The infeasible snapshot names the earliest contradictory pair of
	// original constraints: A (x≡1 mod 2) vs D (x≡2 mod 6).
	sol, _ = st.Solve(3)
	if sol.OK || sol.Conflict != [2]uint64{ids[0], ids[3]} {
		t.Fatalf("v3: got %+v, want conflict [%d %d]", sol, ids[0], ids[3])
	}

	// Deleting A restores the true solution set of the remaining
	// constraints, which a merged-residue-only state could not recover.
	sol, _ = st.Solve(4)
	if !sol.OK || sol.Res.Cmp(big.NewInt(2)) != 0 || sol.Period.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("v4 (after removing A): got %+v, want x≡2 mod 6", sol)
	}

	// Branching a transaction off an old snapshot works too.
	tx := st.BeginAt(1)
	if _, err := tx.Add(big.NewInt(5), big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	v, err := tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	requireMatchesFullMerge(t, st, v)
}

func TestRollbackLeavesStoreUntouched(t *testing.T) {
	st, _ := buildHistory(t)
	latest := st.Latest()
	tx := st.Begin()
	if _, err := tx.Add(big.NewInt(11), big.NewInt(3)); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if st.Latest() != latest {
		t.Fatalf("rollback created a version")
	}
	if _, err := tx.Commit(); err == nil {
		t.Fatalf("commit after rollback must fail")
	}
	requireMatchesFullMerge(t, st, st.Latest())
}

func TestCheckpointRoundTrip(t *testing.T) {
	st, _ := buildHistory(t)
	var buf bytes.Buffer
	if err := st.Save(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest() != st.Latest() {
		t.Fatalf("restored %d versions, want %d", got.Latest()+1, st.Latest()+1)
	}
	for v := 0; v <= st.Latest(); v++ {
		wantSol, _ := st.Solve(v)
		gotSol, err := got.Solve(v)
		if err != nil {
			t.Fatal(err)
		}
		if !sameSolution(wantSol, gotSol) {
			t.Fatalf("version %d: restored %+v, want %+v", v, gotSol, wantSol)
		}
		// Recovery is verified against a full generalized merge.
		requireMatchesFullMerge(t, got, v)
	}

	// Undo still works on the restored store: stage a removal, roll it
	// back, and the latest snapshot must be byte-identical in behavior.
	tx := got.Begin()
	cons, _ := got.Constraints(got.Latest())
	if err := tx.Remove(cons[0].ID); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	requireMatchesFullMerge(t, got, got.Latest())

	// And new commits on the restored store solve correctly.
	tx = got.Begin()
	if _, err := tx.Add(big.NewInt(5), big.NewInt(2)); err != nil {
		t.Fatal(err)
	}
	v, err := tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	requireMatchesFullMerge(t, got, v)
}

func TestCheckpointRejectsCorruptIncrementalState(t *testing.T) {
	st, _ := buildHistory(t)
	var buf bytes.Buffer
	if err := st.Save(&buf); err != nil {
		t.Fatal(err)
	}
	// Tamper with the persisted incremental state of the first solved
	// version: x≡5 mod 12 becomes x≡6 mod 12.
	tampered := bytes.Replace(buf.Bytes(), []byte(`"res": "5"`), []byte(`"res": "6"`), 1)
	if bytes.Equal(tampered, buf.Bytes()) {
		t.Fatal("tampering did not change the checkpoint")
	}
	if _, err := Load(bytes.NewReader(tampered)); err == nil {
		t.Fatal("corrupt checkpoint loaded without full-merge verification error")
	}
}
