package gcrt

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

func buildSampleStore(t *testing.T) *Store {
	t.Helper()
	st := NewStore()
	tx, _ := st.Begin()
	a, _ := tx.Add(big.NewInt(6), big.NewInt(-2)) // x ≡ 4 (mod 6)
	b, _ := tx.Add(big.NewInt(8), big.NewInt(2))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, _ = st.Begin()
	c, _ := tx.Add(big.NewInt(8), big.NewInt(6)) // consistent with a, conflicts with b
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, _ = st.Begin()
	_ = tx.Remove(b) // restore feasibility from originals
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, _ = st.Begin()
	_ = tx.Remove(a)
	_ = tx.Remove(c)
	if _, err := tx.Commit(); err != nil { // empty again: all integers
		t.Fatal(err)
	}
	return st
}

func TestCheckpointRoundTrip(t *testing.T) {
	st := buildSampleStore(t)
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := st.SaveCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version() != st.Version() {
		t.Fatalf("loaded version = %d, want %d", loaded.Version(), st.Version())
	}
	for v := 0; v <= st.Version(); v++ {
		got, err := loaded.Solution(v)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := st.Solution(v)
		if !sameSolution(got, want) {
			t.Fatalf("version %d: loaded %v, want %v", v, got, want)
		}
		// Recovery is audited by a full merge of the persisted originals.
		verifyVersion(t, loaded, v, true)
	}
	// The restored store keeps working: new commits get fresh IDs.
	tx, err := loaded.Begin()
	if err != nil {
		t.Fatal(err)
	}
	id, err := tx.Add(big.NewInt(9), big.NewInt(5))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sol, _ := loaded.Solution(loaded.Version())
	if !sol.OK || sol.Res.Cmp(big.NewInt(5)) != 0 || sol.Period.Cmp(big.NewInt(9)) != 0 {
		t.Fatalf("post-recovery solution = %v, want x ≡ 5 (mod 9)", sol)
	}
	cs, _ := loaded.Constraints(loaded.Version())
	if len(cs) != 1 || cs[0].ID != id {
		t.Fatalf("post-recovery constraints = %v", cs)
	}
}

func TestCheckpointRejectsTamperedState(t *testing.T) {
	st := buildSampleStore(t)
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := st.SaveCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatal(err)
	}
	// Corrupt the cached incremental solution of version 1; the originals
	// still merge to x ≡ 10 (mod 24), so the audit must reject the file.
	cp.Versions[1].Solution.Res = "11"
	data, err = json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCheckpoint(path); err == nil {
		t.Fatal("tampered checkpoint was accepted")
	}
}

func TestCheckpointPreservesIdentitiesAndConflict(t *testing.T) {
	st := buildSampleStore(t)
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := st.SaveCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	// Version 2 is infeasible; the conflicting original pair must survive.
	sol, err := loaded.Solution(2)
	if err != nil {
		t.Fatal(err)
	}
	if sol.OK {
		t.Fatal("version 2 should be infeasible after recovery")
	}
	cs, err := loaded.Constraints(2)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for _, c := range cs {
		ids[c.ID] = true
	}
	if !ids[sol.Conflict[0]] || !ids[sol.Conflict[1]] {
		t.Fatalf("conflict pair %v not among recovered originals %v", sol.Conflict, cs)
	}
	// The recovered conflict must match a full merge of the originals.
	if want := SolveConstraints(cs); want.Conflict != sol.Conflict {
		t.Fatalf("recovered conflict %v != full merge %v", sol.Conflict, want.Conflict)
	}
}
