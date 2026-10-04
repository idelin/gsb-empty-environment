package engine

import (
	"fmt"
	"math/rand"
	"testing"
)

// randExpr builds a random formula. depth bounds nesting.
func randExpr(r *rand.Rand, pool []string, depth int) string {
	if depth <= 0 {
		if r.Intn(2) == 0 {
			return fmt.Sprintf("%d", r.Intn(10))
		}
		return pool[r.Intn(len(pool))]
	}
	switch r.Intn(7) {
	case 0:
		return fmt.Sprintf("%d", r.Intn(20))
	case 1:
		if r.Intn(5) == 0 {
			return "ZZ" // likely-missing reference
		}
		return pool[r.Intn(len(pool))]
	case 2:
		return fmt.Sprintf("%s + %s", randExpr(r, pool, depth-1), randExpr(r, pool, depth-1))
	case 3:
		return fmt.Sprintf("%s - %s", randExpr(r, pool, depth-1), randExpr(r, pool, depth-1))
	case 4:
		return fmt.Sprintf("%s * %s", randExpr(r, pool, depth-1), randExpr(r, pool, depth-1))
	case 5:
		return fmt.Sprintf("IF(%s, %s, %s)", randExpr(r, pool, depth-1), randExpr(r, pool, depth-1), randExpr(r, pool, depth-1))
	default:
		return "#ERR!"
	}
}

// sameValue compares engine and reference values. For cycle errors only
// the code is compared: the evidence is a genuine cycle in both, but its
// exact cell set can legitimately differ in graphs with overlapping
// cycles because the two implementations enter the graph differently.
func sameValue(a, b Value) bool {
	if a.IsErr != b.IsErr {
		return false
	}
	if !a.IsErr {
		return a.Num == b.Num
	}
	if a.Code != b.Code {
		return false
	}
	if a.Code == ErrCycle {
		return len(a.Cycle) > 0 && len(b.Cycle) > 0
	}
	return true
}

func containsStr(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// TestRandomSequencesAgainstReference drives the incremental engine and
// the from-scratch reference through identical random short operation
// sequences, comparing current and historical version queries after every
// batch.
func TestRandomSequencesAgainstReference(t *testing.T) {
	pool := []string{"A", "B", "C", "D", "E", "F"}
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		eng := New()
		model := NewReference()
		snaps := []map[string]Value{{}} // version 0: empty
		seen := map[string]bool{"ZZ": true}
		for _, c := range pool {
			seen[c] = true
		}

		compareVersion := func(ver int) {
			t.Helper()
			for id := range seen {
				ev, eok := eng.Get(id, ver)
				rv, rok := snaps[ver][id]
				if eok != rok {
					t.Fatalf("seed %d v%d %s: found mismatch engine=%v ref=%v", seed, ver, id, eok, rok)
				}
				if !eok {
					continue
				}
				if !sameValue(ev, rv) {
					t.Fatalf("seed %d v%d %s: engine=%v ref=%v", seed, ver, id, ev, rv)
				}
				// Cycle evidence must name cells that are themselves
				// cycle errors in the same version.
				if ev.IsErr && ev.Code == ErrCycle {
					for _, cid := range ev.Cycle {
						cv, _ := eng.Get(cid, ver)
						if !cv.IsErr || cv.Code != ErrCycle {
							t.Fatalf("seed %d v%d %s: evidence cell %s is %v", seed, ver, id, cid, cv)
						}
					}
				}
			}
		}

		for step := 0; step < 25; step++ {
			sets := map[string]string{}
			var dels []string
			n := 1 + r.Intn(3)
			for i := 0; i < n; i++ {
				id := pool[r.Intn(len(pool))]
				if r.Intn(5) == 0 {
					delete(sets, id)
					if !containsStr(dels, id) {
						dels = append(dels, id)
					}
				} else {
					dels = removeStr(dels, id)
					sets[id] = randExpr(r, pool, 2+r.Intn(2))
				}
			}
			if len(sets) == 0 && len(dels) == 0 {
				continue
			}

			rec, err := eng.Apply(sets, dels)
			if err != nil {
				t.Fatalf("seed %d step %d: Apply: %v", seed, step, err)
			}

			refSets := map[string]Expr{}
			for id, src := range sets {
				ex, err := Parse(src)
				if err != nil {
					t.Fatalf("seed %d step %d: Parse(%q): %v", seed, step, src, err)
				}
				refSets[id] = ex
			}
			model.Apply(refSets, dels)

			snap := map[string]Value{}
			for _, id := range model.Cells() {
				snap[id] = model.Get(id)
			}
			snaps = append(snaps, snap)
			if eng.Version() != len(snaps)-1 {
				t.Fatalf("seed %d step %d: version mismatch eng=%d snaps=%d", seed, step, eng.Version(), len(snaps)-1)
			}

			// Every reported recomputed cell must exist in the new version.
			for _, id := range rec {
				if _, ok := eng.Get(id, eng.Version()); !ok {
					t.Fatalf("seed %d step %d: recomputed cell %s missing", seed, step, id)
				}
			}

			compareVersion(eng.Version())
			for k := 0; k < 3; k++ {
				compareVersion(r.Intn(eng.Version() + 1))
			}
		}
	}
}

func removeStr(s []string, x string) []string {
	out := s[:0]
	for _, v := range s {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
