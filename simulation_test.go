package admission

import (
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

func TestRandomGateSimulationAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	for trial := 0; trial < 40; trial++ {
		names := []string{"a", "b", "c"}
		mods := []int64{6, 12, 15}
		pick := func(i int) int64 { return mods[rng.Intn(len(mods))] }
		ms := []int64{pick(0), pick(1), pick(2)}
		vars := make([]Variable, 3)
		for i := range vars {
			vars[i] = Variable{Name: names[i], Modulus: big.NewInt(ms[i])}
		}
		infos, byName, err := buildVariables(vars)
		if err != nil {
			t.Fatal(err)
		}

		nPol := 2 + rng.Intn(2)
		policies := make([]CollusionPolicy, nPol)
		intForms := make([][]internalForm, nPol)
		caps := make([]*big.Rat, nPol)
		for pi := range policies {
			nForms := 1 + rng.Intn(2)
			forms := make([]ProtectedForm, nForms)
			seen := map[int]bool{}
			for fi := 0; fi < nForms; fi++ {
				vi := rng.Intn(3)
				for seen[vi] {
					vi = (vi + 1) % 3
				}
				seen[vi] = true
				forms[fi] = ProtectedForm{
					Name: names[vi], Modulus: big.NewInt(ms[vi]),
					Terms: []Term{{Variable: names[vi], Coefficient: big.NewInt(1)}},
				}
			}
			caps[pi] = Shrink(int64(1+rng.Intn(6)), 1)
			policies[pi] = CollusionPolicy{Name: names[pi%3] + "-group", Protected: forms, MaxShrink: caps[pi]}
			intForms[pi] = make([]internalForm, nForms)
			for fi, f := range forms {
				inf, err := buildForm(f, infos, byName)
				if err != nil {
					t.Fatal(err)
				}
				intForms[pi][fi] = inf
			}
		}
		g, err := NewGate(Config{Variables: vars, Policies: policies})
		if err != nil {
			t.Fatal(err)
		}

		// Brute-force prior projections over the full domain.
		prior := make([]map[string]struct{}, nPol)
		for pi := range prior {
			prior[pi] = map[string]struct{}{}
		}
		bruteFeasible(infos, nil, nil, func(x []uint64) {
			for pi := range intForms {
				prior[pi][formTupleKey(x, intForms[pi])] = struct{}{}
			}
		})
		current := make([][]uint64, 0)
		bruteFeasible(infos, nil, nil, func(x []uint64) { current = append(current, append([]uint64(nil), x...)) })

		for step := 0; step < 6; step++ {
			nRows := 1 + rng.Intn(2)
			rows := make([]ReleaseRow, nRows)
			for ri := 0; ri < nRows; ri++ {
				nm := ms[rng.Intn(3)]
				mod := big.NewInt(nm)
				terms := make([]Term, 3)
				for vi := 0; vi < 3; vi++ {
					need := new(big.Int).Quo(mod, gcdInt(mod, big.NewInt(ms[vi])))
					k := int64(rng.Intn(3) - 1)
					terms[vi] = Term{Variable: names[vi], Coefficient: new(big.Int).Mul(need, big.NewInt(k))}
				}
				rows[ri] = ReleaseRow{ID: "r", Modulus: mod, Terms: terms, Value: big.NewInt(int64(rng.Intn(int(nm))))}
			}
			batch := &Batch{ID: "step", Releases: rows}
			pb, err := buildBatch(batch, infos, byName)
			if err != nil {
				t.Fatal(err)
			}

			next := make([][]uint64, 0)
			for _, x := range current {
				ok := true
				for _, r := range pb.rows {
					if evalFormMod(x, r.terms, r.modulus).Cmp(r.target) != 0 {
						ok = false
						break
					}
				}
				if ok {
					next = append(next, x)
				}
			}
			before := g.State()
			rep, gotErr := g.Admit(batch)
			if len(next) == 0 {
				if !errors.Is(gotErr, ErrInconsistent) {
					t.Fatalf("trial %d step %d: err=%v want inconsistent", trial, step, gotErr)
				}
				if g.State().Digest != before.Digest || g.State().Version != before.Version {
					t.Fatalf("trial %d step %d: inconsistent batch changed state", trial, step)
				}
				continue
			}
			allowed := true
			for pi := range intForms {
				postSet := map[string]struct{}{}
				for _, x := range next {
					postSet[formTupleKey(x, intForms[pi])] = struct{}{}
				}
				ratio := new(big.Rat).SetFrac(big.NewInt(int64(len(prior[pi]))), big.NewInt(int64(len(postSet))))
				if ratio.Cmp(caps[pi]) > 0 {
					allowed = false
				}
			}
			if allowed {
				if gotErr != nil {
					t.Fatalf("trial %d step %d: unexpected err=%v (feasible %d)", trial, step, gotErr, len(next))
				}
				if !rep.Committed || rep.CommittedFeasibleCount.Cmp(big.NewInt(int64(len(next)))) != 0 {
					t.Fatalf("trial %d step %d: feasible=%s brute=%d", trial, step, rep.CommittedFeasibleCount, len(next))
				}
				current = next
			} else {
				if !errors.Is(gotErr, ErrCollusionExceeded) {
					t.Fatalf("trial %d step %d: err=%v want collusion", trial, step, gotErr)
				}
				if g.State().Digest != before.Digest || g.State().Version != before.Version {
					t.Fatalf("trial %d step %d: rejected batch changed state", trial, step)
				}
			}
		}
	}
}
