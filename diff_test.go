package cellengine

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// sameValue compares values; cycle errors are compared by kind only because
// the concrete loop reported for a graph with several overlapping cycles may
// legitimately differ between two traversal orders.
func sameValue(a, b Value) bool {
	if a.IsErr != b.IsErr {
		return false
	}
	if !a.IsErr {
		return a.Num == b.Num
	}
	if a.IsCycleErr() || b.IsCycleErr() {
		return a.IsCycleErr() && b.IsCycleErr()
	}
	return a.Err == b.Err
}

func checkAgainstReference(t *testing.T, e *Engine, version int, cells map[string]string, ctx string) {
	t.Helper()
	ref := Reference(cells)
	snap, ok := e.Snapshot(version)
	if !ok {
		t.Fatalf("%s: no snapshot for version %d", ctx, version)
	}
	if len(snap.Values) != len(ref) {
		t.Fatalf("%s: version %d has %d values, reference has %d", ctx, version, len(snap.Values), len(ref))
	}
	for id, want := range ref {
		got := e.GetAt(version, id)
		if !sameValue(got, want) {
			t.Fatalf("%s: %s@v%d = %s, reference = %s (formula %q)",
				ctx, id, version, got, want, cells[id])
		}
	}
}

func cloneForTest(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var diffNames = []string{"A1", "A2", "B1", "B2", "C1", "C2", "D1", "D2"}

func randFormula(rng *rand.Rand, depth int) string {
	leaf := func() string {
		switch rng.Intn(20) {
		case 0:
			return "#ERR!"
		default:
			if rng.Intn(2) == 0 {
				return fmt.Sprintf("%d", rng.Intn(10))
			}
			return diffNames[rng.Intn(len(diffNames))]
		}
	}
	if depth <= 0 || rng.Intn(100) < 35 {
		return leaf()
	}
	if rng.Intn(100) < 25 {
		return fmt.Sprintf("IF(%s, %s, %s)",
			randFormula(rng, depth-1), randFormula(rng, depth-1), randFormula(rng, depth-1))
	}
	op := []string{"+", "-", "*"}[rng.Intn(3)]
	return fmt.Sprintf("%s %s %s", randFormula(rng, depth-1), op, randFormula(rng, depth-1))
}

func TestRandomSequencesAgainstReference(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1999, 20261004} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			e := New()
			cells := map[string]string{}
			history := []map[string]string{cloneForTest(cells)} // history[v] == cells after version v

			for step := 0; step < 400; step++ {
				ctx := fmt.Sprintf("seed=%d step=%d", seed, step)

				var ops []Op
				n := 1 + rng.Intn(3)
				for i := 0; i < n; i++ {
					id := diffNames[rng.Intn(len(diffNames))]
					if rng.Intn(100) < 15 {
						ops = append(ops, DelOp(id))
						delete(cells, id)
					} else {
						f := randFormula(rng, 3)
						ops = append(ops, SetOp(id, f))
						cells[id] = f
					}
				}
				rec, err := e.Apply(ops...)
				if err != nil {
					t.Fatalf("%s: Apply: %v", ctx, err)
				}
				history = append(history, cloneForTest(cells))

				// 1. Current version matches the from-scratch reference.
				checkAgainstReference(t, e, e.Version(), cells, ctx)

				// 2. A random historical version still matches its reference.
				v := 1 + rng.Intn(e.Version())
				checkAgainstReference(t, e, v, history[v], ctx+" (history)")

				// 3. Cells reported as not recomputed kept their old value.
				if e.Version() >= 2 {
					prev, _ := e.Snapshot(e.Version() - 1)
					recomputed := make(map[string]bool)
					for _, id := range rec {
						recomputed[id] = true
					}
					for id, before := range prev.Values {
						if recomputed[id] {
							continue
						}
						if _, exists := cells[id]; !exists {
							continue // deleted in this batch, not recomputed
						}
						after := e.Get(id)
						if !after.Equal(before) {
							t.Fatalf("%s: %s changed %s -> %s without being recomputed",
								ctx, id, before, after)
						}
					}
				}

				// 4. Every recomputed cell exists in the new version.
				for _, id := range rec {
					if _, ok := cells[id]; !ok {
						t.Fatalf("%s: recomputed cell %s not in sheet", ctx, id)
					}
				}
			}
		})
	}
}

// TestReferenceIsLazyToo pins the reference evaluator's lazy-branch behavior
// so the differential test compares like with like.
func TestReferenceIsLazyToo(t *testing.T) {
	got := Reference(map[string]string{
		"A1": "IF(0, A1, 42)",
		"B1": "IF(1, 7, B1)",
	})
	if got["A1"] != NumVal(42) || got["B1"] != NumVal(7) {
		t.Fatalf("reference must skip unselected branches: %v", got)
	}
	cyc := Reference(map[string]string{
		"A1": "IF(1, B1, 1)",
		"B1": "A1",
	})
	if !cyc["A1"].IsCycleErr() || !cyc["B1"].IsCycleErr() {
		t.Fatalf("reference must detect executed cycles: %v", cyc)
	}
	if !strings.Contains(cyc["A1"].Err, "A1") || !strings.Contains(cyc["A1"].Err, "B1") {
		t.Fatalf("reference cycle evidence must name cells: %q", cyc["A1"].Err)
	}
}
