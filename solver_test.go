package netsec

import (
	"math/rand"
	"testing"
)

// bruteForce is the from-scratch oracle: it enumerates all assignments for
// the given permanent clauses and assumption levels without any learning.
func bruteForce(nvars int, perm [][]Lit, levels [][]Lit) (bool, []bool) {
	satClause := func(mask uint32, lits []Lit) bool {
		for _, l := range lits {
			val := mask&(uint32(1)<<uint(varOf(l)-1)) != 0
			if val == (l > 0) {
				return true
			}
		}
		return false
	}
	for mask := uint32(0); mask < uint32(1)<<uint(nvars); mask++ {
		ok := true
		for _, c := range perm {
			if !satClause(mask, c) {
				ok = false
				break
			}
		}
		if ok {
		levelsLoop:
			for _, lvl := range levels {
				for _, l := range lvl {
					if !satClause(mask, []Lit{l}) {
						ok = false
						break levelsLoop
					}
				}
			}
		}
		if ok {
			model := make([]bool, nvars+1)
			for v := 1; v <= nvars; v++ {
				model[v] = mask&(uint32(1)<<uint(v-1)) != 0
			}
			return true, model
		}
	}
	return false, nil
}

func modelSatisfies(model []bool, perm [][]Lit, levels [][]Lit) bool {
	holds := func(l Lit) bool { return model[varOf(l)] == (l > 0) }
	for _, c := range perm {
		ok := false
		for _, l := range c {
			if holds(l) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, lvl := range levels {
		for _, l := range lvl {
			if !holds(l) {
				return false
			}
		}
	}
	return true
}

func TestNestedAssumptionsCoreAndUndo(t *testing.T) {
	s, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	// (x1 OR x2) AND (NOT x1 OR x2): forces x2.
	if err := s.AddClause(Pos(1), Pos(2)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClause(Neg(1), Pos(2)); err != nil {
		t.Fatal(err)
	}
	lvl0, err := s.Push(Pos(3))
	if err != nil {
		t.Fatal(err)
	}
	if lvl0 != 0 {
		t.Fatalf("first level index = %d, want 0", lvl0)
	}
	if r := s.Solve(); !r.SAT {
		t.Fatal("expected SAT under level 0")
	}
	lvl1, err := s.Push(Neg(2))
	if err != nil {
		t.Fatal(err)
	}
	r := s.Solve()
	if r.SAT {
		t.Fatal("expected UNSAT after assuming NOT x2")
	}
	found := false
	for _, lv := range r.Core {
		if lv == lvl1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("core %v does not contain revoked level %d", r.Core, lvl1)
	}
	s.Pop()
	for _, lc := range s.learned {
		if lc.deps&(uint64(1)<<uint(lvl1)) != 0 {
			t.Fatalf("learned clause %v survived pop of level %d", lc.lits, lvl1)
		}
	}
	r = s.Solve()
	if !r.SAT {
		t.Fatal("expected SAT again after popping the conflicting assumption")
	}
	if !modelSatisfies(r.Model, s.perm, s.levels) {
		t.Fatalf("model %v violates clauses", r.Model)
	}
}

func TestPermanentUnsatHasEmptyCore(t *testing.T) {
	s, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddClause(Pos(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClause(Neg(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Push(Pos(2)); err != nil {
		t.Fatal(err)
	}
	r := s.Solve()
	if r.SAT {
		t.Fatal("expected UNSAT from contradictory permanent clauses")
	}
	if len(r.Core) != 0 {
		t.Fatalf("core %v, want empty (permanent clauses alone are unsat)", r.Core)
	}
}

func TestLiteralValidation(t *testing.T) {
	if _, err := New(MaxVars + 1); err == nil {
		t.Fatal("expected error for too many variables")
	}
	s, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddClause(Pos(3)); err == nil {
		t.Fatal("expected error for out-of-range clause literal")
	}
	if _, err := s.Push(Neg(0)); err == nil {
		t.Fatal("expected error for zero assumption literal")
	}
}

// TestRandomPushPopAgainstScratch drives random AddClause/Push/Pop/Solve
// sequences and compares every Solve against a from-scratch brute-force
// solver over the same permanent clauses and active assumptions. It also
// checks that retained learned clauses never depend on popped levels and
// that reported cores are genuine refusal evidence.
func TestRandomPushPopAgainstScratch(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		nvars := 1 + rng.Intn(8)
		s, err := New(nvars)
		if err != nil {
			t.Fatal(err)
		}
		var perm [][]Lit
		var levels [][]Lit
		randLits := func(k int) []Lit {
			out := make([]Lit, 0, k)
			for i := 0; i < k; i++ {
				l := Pos(1 + rng.Intn(nvars))
				if rng.Intn(2) == 0 {
					l = -l
				}
				out = append(out, l)
			}
			return out
		}
		for step := 0; step < 400; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2:
				c := randLits(1 + rng.Intn(3))
				if err := s.AddClause(c...); err != nil {
					t.Fatalf("seed %d step %d: %v", seed, step, err)
				}
				perm = append(perm, c)
			case 3, 4, 5:
				if len(levels) >= maxLevels {
					continue
				}
				a := randLits(rng.Intn(3))
				if _, err := s.Push(a...); err != nil {
					t.Fatalf("seed %d step %d: %v", seed, step, err)
				}
				levels = append(levels, a)
			case 6, 7:
				if len(levels) > 0 {
					s.Pop()
					levels = levels[:len(levels)-1]
				}
			default:
				res := s.Solve()
				satScratch, _ := bruteForce(nvars, perm, levels)
				if res.SAT != satScratch {
					t.Fatalf("seed %d step %d: incremental SAT=%v, scratch SAT=%v (perm=%v levels=%v)",
						seed, step, res.SAT, satScratch, perm, levels)
				}
				if res.SAT {
					if !modelSatisfies(res.Model, perm, levels) {
						t.Fatalf("seed %d step %d: model %v violates clauses (perm=%v levels=%v)",
							seed, step, res.Model, perm, levels)
					}
				} else {
					var coreLevels [][]Lit
					for _, lv := range res.Core {
						if lv < 0 || lv >= len(levels) {
							t.Fatalf("seed %d step %d: core references inactive level %d", seed, step, lv)
						}
						coreLevels = append(coreLevels, levels[lv])
					}
					if sat, _ := bruteForce(nvars, perm, coreLevels); sat {
						t.Fatalf("seed %d step %d: core %v is not unsat by itself", seed, step, res.Core)
					}
				}
			}
			for _, lc := range s.learned {
				if lc.deps>>uint(len(levels)) != 0 {
					t.Fatalf("seed %d step %d: learned clause %v depends on popped level (deps=%b depth=%d)",
						seed, step, lc.lits, lc.deps, len(levels))
				}
			}
		}
	}
}
