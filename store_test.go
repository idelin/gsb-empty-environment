package gcrt

import (
	"math/big"
	"math/rand"
	"testing"
)

// verifyVersion checks a store version against a full merge of its original
// constraints and, for small moduli, against finite-period enumeration.
func verifyVersion(t *testing.T, st *Store, version int, enumerate bool) {
	t.Helper()
	cs, err := st.Constraints(version)
	if err != nil {
		t.Fatalf("Constraints(%d): %v", version, err)
	}
	sol, err := st.Solution(version)
	if err != nil {
		t.Fatalf("Solution(%d): %v", version, err)
	}
	want := SolveConstraints(cs)
	if !sameSolution(sol, want) {
		t.Fatalf("version %d: cached %v != full merge %v", version, sol, want)
	}
	if enumerate && lcmOf(cs) <= enumLimit {
		checkAgainstEnumeration(t, cs, sol)
	}
}

func TestTransactionsSnapshotsAndRemoval(t *testing.T) {
	st := NewStore()

	// Version 1: x ≡ 4 (mod 6), x ≡ 2 (mod 8) -> x ≡ 10 (mod 24).
	tx, _ := st.Begin()
	id1, _ := tx.Add(big.NewInt(6), big.NewInt(4))
	id2, _ := tx.Add(big.NewInt(8), big.NewInt(2))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sol, _ := st.Solution(1)
	if !sol.OK || sol.Res.Cmp(big.NewInt(10)) != 0 || sol.Period.Cmp(big.NewInt(24)) != 0 {
		t.Fatalf("version 1 = %v, want x ≡ 10 (mod 24)", sol)
	}

	// Version 2: add x ≡ 6 (mod 8) -> consistent with id1 (both even) but
	// conflicts with id2 (2 ≢ 6 mod 8).
	tx, _ = st.Begin()
	id3, _ := tx.Add(big.NewInt(8), big.NewInt(6))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sol, _ = st.Solution(2)
	if sol.OK || sol.Conflict != [2]int64{id2, id3} {
		t.Fatalf("version 2 = %v, want conflict [%d %d]", sol, id2, id3)
	}

	// Version 3: remove id2 -> the true solution set must be restored from
	// the surviving original constraints: x ≡ 4 (mod 6), x ≡ 6 (mod 8),
	// i.e. x ≡ 22 (mod 24). The old merged residue alone could not do this.
	tx, _ = st.Begin()
	if err := tx.Remove(id2); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sol, _ = st.Solution(3)
	if !sol.OK || sol.Res.Cmp(big.NewInt(22)) != 0 || sol.Period.Cmp(big.NewInt(24)) != 0 {
		t.Fatalf("version 3 = %v, want x ≡ 22 (mod 24)", sol)
	}

	// Old snapshots stay queryable and immutable.
	sol, _ = st.Solution(1)
	if sol.Res.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("snapshot version 1 changed: %v", sol)
	}
	sol, _ = st.Solution(2)
	if sol.OK || sol.Conflict != [2]int64{id2, id3} {
		t.Fatalf("snapshot version 2 changed: %v", sol)
	}

	// Version 4: remove id1 as well -> single constraint x ≡ 6 (mod 8).
	tx, _ = st.Begin()
	_ = tx.Remove(id1)
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sol, _ = st.Solution(4)
	if !sol.OK || sol.Res.Cmp(big.NewInt(6)) != 0 || sol.Period.Cmp(big.NewInt(8)) != 0 {
		t.Fatalf("version 4 = %v, want x ≡ 6 (mod 8)", sol)
	}

	// Version 5: remove everything -> all integers again.
	tx, _ = st.Begin()
	_ = tx.Remove(id3)
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sol, _ = st.Solution(5)
	if !sol.OK || sol.Period.Cmp(big.NewInt(1)) != 0 || !sol.Contains(big.NewInt(-12345)) {
		t.Fatalf("version 5 = %v, want all integers", sol)
	}

	for v := 0; v <= st.Version(); v++ {
		verifyVersion(t, st, v, true)
	}
}

func TestRollbackLeavesStoreUntouched(t *testing.T) {
	st := NewStore()
	tx, _ := st.Begin()
	id1, _ := tx.Add(big.NewInt(7), big.NewInt(3))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Solution(1)

	// Open a transaction, buffer changes, then roll back.
	tx, _ = st.Begin()
	if _, err := tx.Add(big.NewInt(5), big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Remove(id1); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()

	if st.Version() != 1 {
		t.Fatalf("version = %d after rollback, want 1", st.Version())
	}
	after, _ := st.Solution(1)
	if !sameSolution(before, after) {
		t.Fatalf("state changed after rollback: %v -> %v", before, after)
	}
	// The full merge of the surviving originals confirms the undo.
	verifyVersion(t, st, 1, true)

	// Operations on a closed transaction must fail.
	if _, err := tx.Add(big.NewInt(3), big.NewInt(0)); err == nil {
		t.Fatal("Add on rolled-back transaction should fail")
	}
	if _, err := tx.Commit(); err == nil {
		t.Fatal("Commit on rolled-back transaction should fail")
	}
}

func TestRandomTransactionalWorkload(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	st := NewStore()
	var live []int64
	for step := 0; step < 60; step++ {
		tx, err := st.Begin()
		if err != nil {
			t.Fatal(err)
		}
		rollback := rng.Intn(4) == 0
		for op := 0; op < 1+rng.Intn(4); op++ {
			if len(live) > 0 && rng.Intn(2) == 0 {
				victim := live[rng.Intn(len(live))]
				if err := tx.Remove(victim); err != nil {
					t.Fatal(err)
				}
				if !rollback {
					live = removeID(live, victim)
				}
			} else {
				id, err := tx.Add(big.NewInt(int64(1+rng.Intn(30))), big.NewInt(int64(rng.Intn(61)-30)))
				if err != nil {
					t.Fatal(err)
				}
				if !rollback {
					live = append(live, id)
				}
			}
		}
		if rollback {
			tx.Rollback()
			continue
		}
		if _, err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		// Verify every historical snapshot against a full merge and, since
		// moduli are small, against finite-period enumeration.
		for v := 0; v <= st.Version(); v++ {
			verifyVersion(t, st, v, true)
		}
	}
}

func removeID(ids []int64, id int64) []int64 {
	out := ids[:0]
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}
