package admission

import (
	"math/big"
	"math/rand"
	"testing"
)

func mustVars(t *testing.T, mods ...int64) ([]variableInfo, map[string]int) {
	t.Helper()
	vs := make([]Variable, len(mods))
	for i, m := range mods {
		vs[i] = Variable{Name: string(rune('a' + i)), Modulus: big.NewInt(m)}
	}
	vars, byName, err := buildVariables(vs)
	if err != nil {
		t.Fatal(err)
	}
	return vars, byName
}

func internalRows(t *testing.T, vars []variableInfo, byName map[string]int, specs []ReleaseRow) []internalRow {
	t.Helper()
	b := &Batch{Releases: specs}
	pb, err := buildBatch(b, vars, byName)
	if err != nil {
		t.Fatal(err)
	}
	return pb.rows
}

func bruteFeasible(vars []variableInfo, rows []internalRow, aux []internalAux, visit func([]uint64)) *big.Int {
	count := new(big.Int)
	x := make([]uint64, len(vars))
	var rec func(i int)
	rec = func(i int) {
		if i == len(vars) {
			for _, r := range rows {
				got := evalFormMod(x, r.terms, r.modulus)
				if got.Cmp(r.target) != 0 {
					return
				}
			}
			for _, a := range aux {
				if !a.eval(x) {
					return
				}
			}
			count.Add(count, big.NewInt(1))
			if visit != nil {
				visit(append([]uint64(nil), x...))
			}
			return
		}
		m := vars[i].modulus.Uint64()
		for v := uint64(0); v < m; v++ {
			x[i] = v
			rec(i + 1)
		}
	}
	rec(0)
	return count
}

func TestCountSystemAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	mods := []int64{6, 12, 15, 8, 9, 10, 14, 18, 21, 25}
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.Intn(3)
		ms := make([]int64, n)
		for i := range ms {
			ms[i] = mods[rng.Intn(len(mods))]
		}
		vars, byName := mustVars(t, ms...)
		nRows := rng.Intn(4)
		specs := make([]ReleaseRow, 0, nRows)
		for ri := 0; ri < nRows; ri++ {
			var nm int64
			switch rng.Intn(3) {
			case 0:
				nm = ms[rng.Intn(n)]
			case 1:
				nm = 6 * int64(1+rng.Intn(3))
			default:
				nm = 15
			}
			mod := big.NewInt(nm)
			terms := make([]Term, 0, n)
			for j, v := range vars {
				need := new(big.Int).Quo(mod, gcdInt(mod, v.modulus))
				k := int64(rng.Intn(5) - 2)
				a := new(big.Int).Mul(need, big.NewInt(k))
				if rng.Intn(3) == 0 {
					a.SetInt64(0)
				}
				terms = append(terms, Term{Variable: vars[j].name, Coefficient: a})
			}
			val := big.NewInt(int64(rng.Intn(int(nm))))
			specs = append(specs, ReleaseRow{ID: "r", Modulus: mod, Terms: terms, Value: val})
		}
		rows := internalRows(t, vars, byName, specs)
		got, err := countSystem(rows, vars)
		if err != nil {
			t.Fatalf("iter %d mods=%v specs=%+v: %v", iter, ms, specs, err)
		}
		want := bruteFeasible(vars, rows, nil, nil)
		if got.consistent {
			if got.kernel.Cmp(want) != 0 {
				t.Fatalf("iter %d mods=%v: kernel %s want %s\nrows=%+v", iter, ms, got.kernel, want, specs)
			}
		} else if want.Sign() != 0 {
			t.Fatalf("iter %d mods=%v: reported inconsistent but brute=%s", iter, ms, want)
		}
	}
}

func TestEnumerateSystemAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	mods := []int64{6, 12, 15}
	for iter := 0; iter < 100; iter++ {
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
				k := int64(rng.Intn(3) - 1)
				a := new(big.Int).Mul(need, big.NewInt(k))
				terms = append(terms, Term{Variable: v.name, Coefficient: a})
			}
			specs = append(specs, ReleaseRow{ID: "r", Modulus: mod, Terms: terms, Value: big.NewInt(int64(rng.Intn(int(nm))))})
		}
		rows := internalRows(t, vars, byName, specs)
		want := bruteFeasible(vars, rows, nil, nil)
		got, err := enumerateSystem(rows, vars, 0, enumCallbacks{onPoint: func(x []uint64) bool {
			for _, r := range rows {
				if evalFormMod(x, r.terms, r.modulus).Cmp(r.target) != 0 {
					t.Fatalf("enumerator returned non-solution")
				}
			}
			return true
		}})
		if err != nil {
			t.Fatalf("iter %d mods=%v specs=%+v: %v", iter, ms, specs, err)
		}
		if got.feasible.Cmp(want) != 0 {
			t.Fatalf("iter %d mods=%v: enumerated %s want %s\nrows=%+v", iter, ms, got.feasible, want, specs)
		}
	}
}
