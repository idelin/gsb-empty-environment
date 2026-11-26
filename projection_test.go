package admission

import (
	"math/big"
	"math/rand"
	"testing"
)

func bruteProjection(vars []variableInfo, rows []internalRow, forms []internalForm) *big.Int {
	set := make(map[string]struct{})
	bruteFeasible(vars, rows, nil, func(x []uint64) {
		set[formTupleKey(x, forms)] = struct{}{}
	})
	return big.NewInt(int64(len(set)))
}

func TestJointProjectionAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(21))
	mods := []int64{6, 12, 15}
	for iter := 0; iter < 250; iter++ {
		n := 1 + rng.Intn(3)
		ms := make([]int64, n)
		for i := range ms {
			ms[i] = mods[rng.Intn(len(mods))]
		}
		vars, byName := mustVars(t, ms...)
		nRows := rng.Intn(3)
		specs := make([]ReleaseRow, 0, nRows)
		for ri := 0; ri < nRows; ri++ {
			nm := ms[rng.Intn(n)]
			mod := big.NewInt(nm)
			terms := make([]Term, 0, n)
			for _, v := range vars {
				need := new(big.Int).Quo(mod, gcdInt(mod, v.modulus))
				k := int64(rng.Intn(5) - 2)
				a := new(big.Int).Mul(need, big.NewInt(k))
				terms = append(terms, Term{Variable: v.name, Coefficient: a})
			}
			specs = append(specs, ReleaseRow{ID: "r", Modulus: mod, Terms: terms, Value: big.NewInt(int64(rng.Intn(int(nm))))})
		}
		rows := internalRows(t, vars, byName, specs)

		nForms := 1 + rng.Intn(2)
		forms := make([]internalForm, nForms)
		for fi := 0; fi < nForms; fi++ {
			qm := ms[rng.Intn(n)]
			q := big.NewInt(qm)
			terms := make([]Term, 0, n)
			for _, v := range vars {
				need := new(big.Int).Quo(q, gcdInt(q, v.modulus))
				k := int64(rng.Intn(3) - 1)
				terms = append(terms, Term{Variable: v.name, Coefficient: new(big.Int).Mul(need, big.NewInt(k))})
			}
			f, err := buildForm(ProtectedForm{Name: "f", Modulus: q, Terms: terms}, vars, byName)
			if err != nil {
				t.Fatal(err)
			}
			forms[fi] = f
		}
		got, _, _, err := exactProjectionCount(rows, vars, forms)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		want := bruteProjection(vars, rows, forms)
		if got.Cmp(want) != 0 {
			t.Fatalf("iter %d mods=%v: post projection %s want %s\nrows=%+v forms=%+v",
				iter, ms, got, want, specs, forms)
		}
	}
}

func TestPriorProjectionAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(22))
	mods := []int64{6, 12, 15}
	for iter := 0; iter < 100; iter++ {
		n := 1 + rng.Intn(3)
		ms := make([]int64, n)
		for i := range ms {
			ms[i] = mods[rng.Intn(len(mods))]
		}
		vars, byName := mustVars(t, ms...)
		nForms := 1 + rng.Intn(2)
		forms := make([]internalForm, nForms)
		for fi := 0; fi < nForms; fi++ {
			q := big.NewInt(ms[rng.Intn(n)])
			terms := make([]Term, 0, n)
			for _, v := range vars {
				need := new(big.Int).Quo(q, gcdInt(q, v.modulus))
				terms = append(terms, Term{Variable: v.name, Coefficient: new(big.Int).Mul(need, big.NewInt(int64(rng.Intn(3)-1)))})
			}
			f, err := buildForm(ProtectedForm{Name: "f", Modulus: q, Terms: terms}, vars, byName)
			if err != nil {
				t.Fatal(err)
			}
			forms[fi] = f
		}
		got, _, _, err := exactProjectionCount(nil, vars, forms)
		if err != nil {
			t.Fatal(err)
		}
		want := bruteProjection(vars, nil, forms)
		if got.Cmp(want) != 0 {
			t.Fatalf("iter %d: prior projection %s want %s", iter, got, want)
		}
	}
}
