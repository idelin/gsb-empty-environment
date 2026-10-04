package cellengine

import (
	"reflect"
	"strings"
	"testing"
)

func mustApply(t *testing.T, e *Engine, ops ...Op) []string {
	t.Helper()
	rec, err := e.Apply(ops...)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return rec
}

func wantNum(t *testing.T, e *Engine, id string, want int64) {
	t.Helper()
	got := e.Get(id)
	if got.IsErr || got.Num != want {
		t.Fatalf("%s: got %s, want %d", id, got, want)
	}
}

func wantErr(t *testing.T, e *Engine, id, prefix string) Value {
	t.Helper()
	got := e.Get(id)
	if !got.IsErr || !strings.HasPrefix(got.Err, prefix) {
		t.Fatalf("%s: got %s, want error with prefix %q", id, got, prefix)
	}
	return got
}

func TestBasicArithmeticAndPrecedence(t *testing.T) {
	e := New()
	mustApply(t, e,
		SetOp("A1", "2 + 3 * 4"),
		SetOp("A2", "(2 + 3) * 4"),
		SetOp("A3", "-A1 + 100"),
		SetOp("A4", "=A1 - A2 - 1"),
	)
	wantNum(t, e, "A1", 14)
	wantNum(t, e, "A2", 20)
	wantNum(t, e, "A3", 86)
	wantNum(t, e, "A4", -7)
}

func TestConditionalCycleIntroduceAndRemove(t *testing.T) {
	e := New()
	// A1 reads D1 only when B1 == 0; D1 reads A1. With B1 = 1 the cycle is
	// latent in the unselected branch and must not be an error.
	rec := mustApply(t, e,
		SetOp("B1", "1"),
		SetOp("C1", "10"),
		SetOp("D1", "A1 + 1"),
		SetOp("A1", "IF(B1, C1, D1)"),
	)
	if !reflect.DeepEqual(rec, []string{"A1", "B1", "C1", "D1"}) {
		t.Fatalf("v1 recomputed = %v", rec)
	}
	wantNum(t, e, "A1", 10)
	wantNum(t, e, "D1", 11)
	if got := e.Deps("A1"); !reflect.DeepEqual(got, []string{"B1", "C1"}) {
		t.Fatalf("A1 deps = %v, want [B1 C1] (D1 not read)", got)
	}

	// Flip the flag: A1 now reads D1, closing a real cycle A1 -> D1 -> A1.
	rec = mustApply(t, e, SetOp("B1", "0"))
	if !reflect.DeepEqual(rec, []string{"A1", "B1", "D1"}) {
		t.Fatalf("v2 recomputed = %v, want [A1 B1 D1]", rec)
	}
	v := wantErr(t, e, "A1", "#CYCLE!(")
	if !strings.Contains(v.Err, "A1") || !strings.Contains(v.Err, "D1") {
		t.Fatalf("cycle evidence %q must name A1 and D1", v.Err)
	}
	wantErr(t, e, "D1", "#CYCLE!(")
	wantNum(t, e, "C1", 10) // untouched
	if got := e.Deps("A1"); !reflect.DeepEqual(got, []string{"B1", "D1"}) {
		t.Fatalf("A1 deps after flip = %v, want [B1 D1]", got)
	}

	// Flip back: the cycle disappears and values are restored.
	mustApply(t, e, SetOp("B1", "1"))
	wantNum(t, e, "A1", 10)
	wantNum(t, e, "D1", 11)
}

func TestUnexecutedBranchSelfCycleIsNotError(t *testing.T) {
	e := New()
	mustApply(t, e, SetOp("A1", "IF(0, A1, 42)"))
	wantNum(t, e, "A1", 42)
	mustApply(t, e, SetOp("A1", "IF(1, 7, A1)"))
	wantNum(t, e, "A1", 7)
	// Selecting the self-referencing branch turns it into a real cycle.
	mustApply(t, e, SetOp("A1", "IF(1, A1, 7)"))
	v := wantErr(t, e, "A1", "#CYCLE!(")
	if !strings.Contains(v.Err, "A1->A1") {
		t.Fatalf("self cycle evidence = %q", v.Err)
	}
}

func TestErrorPropagationAlongDeps(t *testing.T) {
	e := New()
	mustApply(t, e,
		SetOp("A1", "#ERR!"),
		SetOp("B1", "A1 + 1"),
		SetOp("C1", "B1 * 3"),
		SetOp("D1", "IF(0, A1, 7)"), // error in unselected branch: not propagated
		SetOp("E1", "IF(A1, 1, 2)"), // error in condition: propagated
	)
	wantErr(t, e, "A1", "#ERR!")
	wantErr(t, e, "B1", "#ERR!")
	wantErr(t, e, "C1", "#ERR!")
	wantNum(t, e, "D1", 7)
	wantErr(t, e, "E1", "#ERR!")

	// Fixing the root clears the whole chain.
	mustApply(t, e, SetOp("A1", "5"))
	wantNum(t, e, "B1", 6)
	wantNum(t, e, "C1", 18)
	wantNum(t, e, "E1", 1)
}

func TestMissingRefDeleteAndReAdd(t *testing.T) {
	e := New()
	mustApply(t, e, SetOp("A1", "B1 + 1"))
	v := wantErr(t, e, "A1", "#REF!(")
	if !strings.Contains(v.Err, "B1") {
		t.Fatalf("#REF! evidence must name B1: %q", v.Err)
	}
	// The missing reference is still a dependency: filling B1 re-evaluates A1.
	rec := mustApply(t, e, SetOp("B1", "5"))
	if !reflect.DeepEqual(rec, []string{"A1", "B1"}) {
		t.Fatalf("recomputed after filling B1 = %v", rec)
	}
	wantNum(t, e, "A1", 6)
	// Deleting B1 re-breaks A1...
	rec = mustApply(t, e, DelOp("B1"))
	if !reflect.DeepEqual(rec, []string{"A1"}) {
		t.Fatalf("recomputed after deleting B1 = %v", rec)
	}
	wantErr(t, e, "A1", "#REF!(")
	wantErr(t, e, "B1", "#REF!(") // querying a deleted cell
	// ...and re-adding it with a new value repairs A1 again.
	mustApply(t, e, SetOp("B1", "7"))
	wantNum(t, e, "A1", 8)
}

func TestDiamondSharedSubgraph(t *testing.T) {
	e := New()
	rec := mustApply(t, e,
		SetOp("A1", "1"),
		SetOp("B1", "A1 + 1"),
		SetOp("C1", "A1 + 2"),
		SetOp("D1", "B1 * C1"),
	)
	if !reflect.DeepEqual(rec, []string{"A1", "B1", "C1", "D1"}) {
		t.Fatalf("v1 recomputed = %v", rec)
	}
	wantNum(t, e, "D1", 6)

	// Changing the shared source recomputes the whole diamond, each cell once.
	rec = mustApply(t, e, SetOp("A1", "10"))
	if !reflect.DeepEqual(rec, []string{"A1", "B1", "C1", "D1"}) {
		t.Fatalf("v2 recomputed = %v", rec)
	}
	wantNum(t, e, "B1", 11)
	wantNum(t, e, "C1", 12)
	wantNum(t, e, "D1", 132)

	// Changing a leaf recomputes only that leaf.
	rec = mustApply(t, e, SetOp("D1", "B1 + C1"))
	if !reflect.DeepEqual(rec, []string{"D1"}) {
		t.Fatalf("v3 recomputed = %v", rec)
	}
	wantNum(t, e, "D1", 23)
}

func TestBatchSwapDependencies(t *testing.T) {
	e := New()
	mustApply(t, e,
		SetOp("P1", "1"),
		SetOp("Q1", "2"),
		SetOp("X1", "P1"),
		SetOp("Y1", "Q1"),
	)
	wantNum(t, e, "X1", 1)
	wantNum(t, e, "Y1", 2)

	// Swap both dependencies in one batch: the new version sees both.
	rec := mustApply(t, e, SetOp("X1", "Q1"), SetOp("Y1", "P1"))
	if !reflect.DeepEqual(rec, []string{"X1", "Y1"}) {
		t.Fatalf("swap recomputed = %v", rec)
	}
	wantNum(t, e, "X1", 2)
	wantNum(t, e, "Y1", 1)

	// A reader of two cells changed in one batch sees them together,
	// never an intermediate mix.
	mustApply(t, e, SetOp("S1", "P1 + Q1"))
	wantNum(t, e, "S1", 3)
	mustApply(t, e, SetOp("P1", "10"), SetOp("Q1", "20"))
	wantNum(t, e, "S1", 30)
}

func TestOldVersionQueriesAreStable(t *testing.T) {
	e := New()
	mustApply(t, e, SetOp("A1", "1"), SetOp("B1", "A1 * 10"))
	mustApply(t, e, SetOp("A1", "2"))
	mustApply(t, e, DelOp("B1"))

	if got := e.GetAt(1, "A1"); got.IsErr || got.Num != 1 {
		t.Fatalf("A1@v1 = %s, want 1", got)
	}
	if got := e.GetAt(1, "B1"); got.IsErr || got.Num != 10 {
		t.Fatalf("B1@v1 = %s, want 10", got)
	}
	if got := e.GetAt(2, "B1"); got.IsErr || got.Num != 20 {
		t.Fatalf("B1@v2 = %s, want 20", got)
	}
	if got := e.GetAt(3, "B1"); !got.IsErr {
		t.Fatalf("B1@v3 = %s, want #REF!", got)
	}
	// Historical snapshots are immutable even after more batches.
	mustApply(t, e, SetOp("B1", "99"))
	if got := e.GetAt(1, "B1"); got.IsErr || got.Num != 10 {
		t.Fatalf("B1@v1 changed to %s", got)
	}
}

func TestCacheNotReusedAcrossVersions(t *testing.T) {
	e := New()
	mustApply(t, e, SetOp("A1", "1"), SetOp("B1", "A1"), SetOp("C1", "B1"))
	wantNum(t, e, "C1", 1)
	// A stale cache would keep B1/C1 at 1 after A1 changes.
	rec := mustApply(t, e, SetOp("A1", "2"))
	if !reflect.DeepEqual(rec, []string{"A1", "B1", "C1"}) {
		t.Fatalf("recomputed = %v", rec)
	}
	wantNum(t, e, "B1", 2)
	wantNum(t, e, "C1", 2)
	// Cells that do not depend on the change keep their cached value and
	// are not recomputed.
	mustApply(t, e, SetOp("Z1", "5"))
	rec = mustApply(t, e, SetOp("A1", "3"))
	if !reflect.DeepEqual(rec, []string{"A1", "B1", "C1"}) {
		t.Fatalf("recomputed = %v, Z1 must not be recomputed", rec)
	}
	wantNum(t, e, "Z1", 5)
}

func TestParseErrors(t *testing.T) {
	e := New()
	mustApply(t, e,
		SetOp("A1", "1 +"),
		SetOp("B1", "A1 * 2"),
		SetOp("C1", "IF(1, 2)"),
	)
	wantErr(t, e, "A1", "#PARSE!(")
	wantErr(t, e, "B1", "#PARSE!(") // propagated
	wantErr(t, e, "C1", "#PARSE!(")
	mustApply(t, e, SetOp("A1", "1 + 1"))
	wantNum(t, e, "A1", 2)
	wantNum(t, e, "B1", 4)
}

func TestCycleEvidenceNamesConcreteCells(t *testing.T) {
	e := New()
	mustApply(t, e,
		SetOp("A1", "B1 + 1"),
		SetOp("B1", "C1 + 1"),
		SetOp("C1", "A1 + 1"),
		SetOp("D1", "A1 * 2"), // downstream of the cycle
	)
	for _, id := range []string{"A1", "B1", "C1"} {
		v := wantErr(t, e, id, "#CYCLE!(")
		for _, named := range []string{"A1", "B1", "C1"} {
			if !strings.Contains(v.Err, named) {
				t.Fatalf("%s: evidence %q missing %s", id, v.Err, named)
			}
		}
	}
	wantErr(t, e, "D1", "#CYCLE!(") // propagated downstream
	// Breaking the cycle anywhere repairs the whole loop.
	mustApply(t, e, SetOp("C1", "0"))
	wantNum(t, e, "A1", 2)
	wantNum(t, e, "B1", 1)
	wantNum(t, e, "D1", 4)
}
