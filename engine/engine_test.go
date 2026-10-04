package engine

import (
	"reflect"
	"testing"
)

func mustApply(t *testing.T, e *Engine, sets map[string]string, dels ...string) []string {
	t.Helper()
	rec, err := e.Apply(sets, dels)
	if err != nil {
		t.Fatalf("Apply(%v, %v): %v", sets, dels, err)
	}
	return rec
}

func getVal(t *testing.T, e *Engine, id string) Value {
	t.Helper()
	v, ok := e.Get(id, e.Version())
	if !ok {
		t.Fatalf("cell %s not found", id)
	}
	return v
}

func wantInt(t *testing.T, e *Engine, id string, n int) {
	t.Helper()
	if v := getVal(t, e, id); v.IsErr || v.Num != n {
		t.Fatalf("%s: want %d, got %v", id, n, v)
	}
}

func wantErr(t *testing.T, e *Engine, id string, code ErrCode) Value {
	t.Helper()
	v := getVal(t, e, id)
	if !v.IsErr || v.Code != code {
		t.Fatalf("%s: want %s, got %v", id, code, v)
	}
	return v
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func wantCycleCells(t *testing.T, v Value, cells ...string) {
	t.Helper()
	if !reflect.DeepEqual(sortedCopy(v.Cycle), cells) {
		t.Fatalf("cycle evidence: want cells %v, got %v", cells, v.Cycle)
	}
}

// checkCycleEvidence verifies that the cycle evidence of every cell
// named in cyc is genuine: each cell's executed reads include the next
// cell on the cycle (and the last reads the first).
func checkCycleEvidence(t *testing.T, e *Engine, cyc []string) {
	t.Helper()
	for i, id := range cyc {
		next := cyc[(i+1)%len(cyc)]
		src, ok := e.Formula(id)
		if !ok {
			t.Fatalf("evidence cell %s has no formula", id)
		}
		ex, err := Parse(src)
		if err != nil {
			t.Fatalf("reparse %s: %v", id, err)
		}
		reads := map[string]bool{}
		evalExpr(ex, func(ref string) Value {
			reads[ref] = true
			v, _ := e.Get(ref, e.Version())
			return v
		})
		if !reads[next] {
			t.Fatalf("evidence %v: %s does not read %s", cyc, id, next)
		}
	}
}

func wantRecomputed(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recomputed: want %v, got %v", want, got)
	}
}

// Switching an IF condition must introduce and later dissolve a real cycle.
func TestConditionalSwitchIntroducesAndRemovesCycle(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{
		"FLAG": "0",
		"A":    "IF(FLAG, B, 1)",
		"B":    "A + 1",
	})
	wantInt(t, e, "A", 1)
	wantInt(t, e, "B", 2)

	// FLAG=1 makes A read B while B reads A: a real cycle.
	mustApply(t, e, map[string]string{"FLAG": "1"})
	wantCycleCells(t, wantErr(t, e, "A", ErrCycle), "A", "B")
	wantCycleCells(t, wantErr(t, e, "B", ErrCycle), "A", "B")
	checkCycleEvidence(t, e, []string{"A", "B"})

	// FLAG=0 dissolves the cycle again.
	mustApply(t, e, map[string]string{"FLAG": "0"})
	wantInt(t, e, "A", 1)
	wantInt(t, e, "B", 2)
}

// A cycle reachable only through an unexecuted branch is not an error.
func TestUnexecutedBranchCycleIsNotAnError(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{
		"A": "IF(0, B, 42)", // never reads B
		"B": "B + 1",        // genuine self-cycle
	})
	wantInt(t, e, "A", 42)
	wantCycleCells(t, wantErr(t, e, "B", ErrCycle), "B")

	// Self-reference in the unexecuted branch.
	mustApply(t, e, map[string]string{"C": "IF(0, C + 1, 7)"})
	wantInt(t, e, "C", 7)

	// Cycle behind an unexecuted branch of a chain.
	mustApply(t, e, map[string]string{
		"D": "IF(0, E, 9)",
		"E": "F + 1",
		"F": "E * 2",
	})
	wantInt(t, e, "D", 9)
	wantCycleCells(t, wantErr(t, e, "E", ErrCycle), "E", "F")
	wantCycleCells(t, wantErr(t, e, "F", ErrCycle), "E", "F")
	checkCycleEvidence(t, e, []string{"E", "F"})
}

// Errors propagate along executed dependencies only.
func TestErrorPropagation(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{
		"A": "M + 1",        // M missing -> #REF!
		"B": "A * 3",        // propagates
		"C": "IF(2, B, 0)",  // cond non-zero -> reads B -> propagates
		"D": "IF(0, B, 99)", // B in unexecuted branch -> no error
		"E": "#ERR!",
		"F": "E + 1",
	})
	wantErr(t, e, "A", ErrRef)
	wantErr(t, e, "B", ErrRef)
	wantErr(t, e, "C", ErrRef)
	wantInt(t, e, "D", 99)
	wantErr(t, e, "E", ErrLiteral)
	wantErr(t, e, "F", ErrLiteral)

	// Providing the missing cell clears the propagated errors.
	rec := mustApply(t, e, map[string]string{"M": "4"})
	wantRecomputed(t, rec, "A", "B", "C", "M")
	wantInt(t, e, "A", 5)
	wantInt(t, e, "B", 15)
	wantInt(t, e, "C", 15)
	wantInt(t, e, "D", 99)
}

// Deleting a referenced cell errors its dependents; re-adding re-evaluates.
func TestDeleteAndReaddReference(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{"A": "B + 1", "B": "2"})
	wantInt(t, e, "A", 3)

	rec := mustApply(t, e, nil, "B")
	wantRecomputed(t, rec, "A")
	wantErr(t, e, "A", ErrRef)
	if _, ok := e.Get("B", e.Version()); ok {
		t.Fatalf("B should be deleted")
	}

	rec = mustApply(t, e, map[string]string{"B": "10"})
	wantRecomputed(t, rec, "A", "B")
	wantInt(t, e, "A", 11)
}

// Diamond-shaped shared subgraph: each affected cell recomputed once.
func TestDiamondSharedSubgraph(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{
		"A": "1",
		"B": "A + 1",
		"C": "A + 1",
		"D": "B + C",
	})
	wantInt(t, e, "D", 4)

	rec := mustApply(t, e, map[string]string{"A": "2"})
	wantRecomputed(t, rec, "A", "B", "C", "D")
	wantInt(t, e, "D", 6)

	// Rewiring D alone touches only D.
	rec = mustApply(t, e, map[string]string{"D": "B * C"})
	wantRecomputed(t, rec, "D")
	wantInt(t, e, "D", 9)
}

// All changes in a batch observe the same new version.
func TestBatchAtomicVisibilityAndSwap(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{"A": "1", "B": "A + 1"})
	wantInt(t, e, "B", 2)

	// C must see the new A within the same batch.
	mustApply(t, e, map[string]string{"A": "10", "C": "A * 2"})
	wantInt(t, e, "C", 20)
	wantInt(t, e, "B", 11)

	// Dependency swap: X used to read Y; after the batch Y reads X.
	mustApply(t, e, map[string]string{"X": "Y + 1", "Y": "5"})
	wantInt(t, e, "X", 6)
	mustApply(t, e, map[string]string{"X": "3", "Y": "X"})
	wantInt(t, e, "X", 3)
	wantInt(t, e, "Y", 3) // Y sees the new X, not the old 6

	// A batch can create a cycle and a later batch can resolve it.
	mustApply(t, e, map[string]string{"P": "Q + 1", "Q": "1"})
	wantInt(t, e, "P", 2)
	mustApply(t, e, map[string]string{"Q": "P + 1"})
	wantCycleCells(t, wantErr(t, e, "P", ErrCycle), "P", "Q")
	wantCycleCells(t, wantErr(t, e, "Q", ErrCycle), "P", "Q")
	checkCycleEvidence(t, e, []string{"P", "Q"})
	mustApply(t, e, map[string]string{"P": "10", "Q": "P * 2"})
	wantInt(t, e, "P", 10)
	wantInt(t, e, "Q", 20)
}

// Old versions stay queryable; cached values must not leak across versions.
func TestVersionedQueries(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{"A": "1", "B": "A + 1"}) // v1
	mustApply(t, e, map[string]string{"A": "5"})               // v2
	mustApply(t, e, nil, "B")                                  // v3

	if _, ok := e.Get("A", 0); ok {
		t.Fatalf("A@v0 should not exist")
	}
	if v, _ := e.Get("A", 1); v.Num != 1 {
		t.Fatalf("A@v1: want 1, got %v", v)
	}
	if v, _ := e.Get("B", 1); v.Num != 2 {
		t.Fatalf("B@v1: want 2, got %v", v)
	}
	if v, _ := e.Get("A", 2); v.Num != 5 {
		t.Fatalf("A@v2: want 5, got %v", v)
	}
	if v, _ := e.Get("B", 2); v.Num != 6 {
		t.Fatalf("B@v2: want 6, got %v", v)
	}
	if _, ok := e.Get("B", 3); ok {
		t.Fatalf("B@v3 should be deleted")
	}
	if v, _ := e.Get("A", 3); v.Num != 5 {
		t.Fatalf("A@v3: want 5, got %v", v)
	}
}

// Only genuinely affected cells are reported as recomputed.
func TestRecomputedMinimality(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{"A": "1", "B": "A + 1", "C": "5"})
	if rec := mustApply(t, e, map[string]string{"C": "6"}); !reflect.DeepEqual(rec, []string{"C"}) {
		t.Fatalf("want [C], got %v", rec)
	}
	if rec := mustApply(t, e, map[string]string{"A": "2"}); !reflect.DeepEqual(rec, []string{"A", "B"}) {
		t.Fatalf("want [A B], got %v", rec)
	}
}

// A bad formula aborts the whole batch without bumping the version.
func TestBatchParseErrorIsAtomic(t *testing.T) {
	e := New()
	mustApply(t, e, map[string]string{"A": "1"})
	v := e.Version()
	if _, err := e.Apply(map[string]string{"A": "1 +", "B": "2"}, nil); err == nil {
		t.Fatalf("expected parse error")
	}
	if e.Version() != v {
		t.Fatalf("version bumped on failed batch")
	}
	if _, ok := e.Get("B", e.Version()); ok {
		t.Fatalf("B must not exist after failed batch")
	}
	if got := getVal(t, e, "A"); got.Num != 1 {
		t.Fatalf("A changed after failed batch: %v", got)
	}
}
