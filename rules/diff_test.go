package rules

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// randomOps builds a deterministic pseudo-random op stream from a small
// id/payload pool so duplicates, conflicting payloads, early retractions
// and winner retractions all occur frequently.
func randomOps(r *rand.Rand, n int) []Op {
	ops := make([]Op, 0, n)
	for i := 0; i < n; i++ {
		if r.Intn(100) < 30 {
			// Retract a random id, whether or not it was ever added.
			ops = append(ops, Retract(fmt.Sprintf("ev-%d", r.Intn(12))))
			continue
		}
		ev := Event{
			ObjectID: fmt.Sprintf("obj-%d", r.Intn(4)),
			EventID:  fmt.Sprintf("ev-%d", r.Intn(12)),
			Priority: r.Intn(4),
			Seq:      int64(r.Intn(6)),
			Payload:  fmt.Sprintf("p-%d", r.Intn(5)),
		}
		ops = append(ops, Add(ev))
	}
	return ops
}

// TestEngineMatchesModel is a differential test: the incremental engine
// and the full-candidate-sorting reference model must emit identical
// delta streams and agree on duplicate/conflict counters.
func TestEngineMatchesModel(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		ops := randomOps(r, 400)

		eng := NewEngine()
		mod := NewModel()
		for i, op := range ops {
			gotEng := eng.Apply(op)
			gotMod := mod.Apply(op)
			if !reflect.DeepEqual(gotEng, gotMod) {
				t.Fatalf("seed %d op %d (%+v): engine deltas %+v != model deltas %+v",
					seed, i, op, gotEng, gotMod)
			}
		}
		if eng.Duplicates != mod.Duplicates || eng.Conflicts != mod.Conflicts {
			t.Fatalf("seed %d: engine dup/conflict = %d/%d, model = %d/%d",
				seed, eng.Duplicates, eng.Conflicts, mod.Duplicates, mod.Conflicts)
		}
		for _, obj := range []string{"obj-0", "obj-1", "obj-2", "obj-3"} {
			we, oke := eng.Effective(obj)
			wm, okm := mod.Effective(obj)
			if oke != okm || we != wm {
				t.Fatalf("seed %d obj %s: engine effective %+v (%v) != model %+v (%v)",
					seed, obj, we, oke, wm, okm)
			}
		}
	}
}
