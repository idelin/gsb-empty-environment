package tablegen

import (
	"math/rand"
	"testing"
)

// randComposition splits total into k non-negative parts.
func randComposition(rng *rand.Rand, total, k int) []int {
	parts := make([]int, k)
	for i := 0; i < k-1; i++ {
		parts[i] = rng.Intn(total + 1)
		total -= parts[i]
	}
	parts[k-1] = total
	return parts
}

func randSpec(rng *rand.Rand, maxDim, maxTotal int) Spec {
	rows := 1 + rng.Intn(maxDim)
	cols := 1 + rng.Intn(maxDim)
	total := rng.Intn(maxTotal + 1)
	spec := Spec{
		RowTotals: randComposition(rng, total, rows),
		ColTotals: randComposition(rng, total, cols),
		Forbidden: make(map[Cell]bool),
	}
	for i := 0; i < rows; i++ {
		for j := 0; j < cols; j++ {
			if rng.Intn(3) == 0 {
				spec.Forbidden[Cell{Row: i, Col: j}] = true
			}
		}
	}
	return spec
}

func checkTable(t *testing.T, spec Spec, tab Table) {
	t.Helper()
	for i, row := range tab {
		sum := 0
		for j, v := range row {
			if v < 0 {
				t.Fatalf("negative count at (%d,%d)", i, j)
			}
			if spec.Forbidden[Cell{Row: i, Col: j}] && v != 0 {
				t.Fatalf("forbidden cell (%d,%d) has count %d", i, j, v)
			}
			sum += v
		}
		if sum != spec.RowTotals[i] {
			t.Fatalf("row %d sums to %d, want %d", i, sum, spec.RowTotals[i])
		}
	}
	for j := range spec.ColTotals {
		sum := 0
		for i := range tab {
			sum += tab[i][j]
		}
		if sum != spec.ColTotals[j] {
			t.Fatalf("col %d sums to %d, want %d", j, sum, spec.ColTotals[j])
		}
	}
}

// checkCertificate verifies a Hall-style witness: every column usable by the
// indicted rows is listed, and listed demand strictly exceeds listed supply.
func checkCertificate(t *testing.T, spec Spec, ev *Evidence) {
	t.Helper()
	if ev.Demand <= ev.Supply {
		t.Fatalf("certificate not violated: demand %d <= supply %d", ev.Demand, ev.Supply)
	}
	demand, supply := 0, 0
	inCols := make(map[int]bool, len(ev.Cols))
	for _, j := range ev.Cols {
		inCols[j] = true
		supply += spec.ColTotals[j]
	}
	for _, i := range ev.Rows {
		demand += spec.RowTotals[i]
		for j := range spec.ColTotals {
			if !spec.Forbidden[Cell{Row: i, Col: j}] && !inCols[j] {
				t.Fatalf("row %d may use unlisted column %d", i, j)
			}
		}
	}
	if demand != ev.Demand || supply != ev.Supply {
		t.Fatalf("certificate totals inconsistent: got (%d,%d), computed (%d,%d)",
			ev.Demand, ev.Supply, demand, supply)
	}
}

func TestSolveRandomSpecs(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		spec := randSpec(rng, 4, 20)
		tab, err := Solve(spec)
		if err != nil {
			ev, ok := err.(*Evidence)
			if !ok {
				t.Fatalf("trial %d: unexpected error %v", trial, err)
			}
			checkCertificate(t, spec, ev)
			continue
		}
		checkTable(t, spec, tab)
	}
}

// bruteFeasible enumerates every table meeting the marginals and structural
// zeros; it is the ground truth for small instances.
func bruteFeasible(spec Spec) bool {
	rows := len(spec.RowTotals)
	cols := len(spec.ColTotals)
	rowLeft := append([]int(nil), spec.RowTotals...)
	colLeft := append([]int(nil), spec.ColTotals...)
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		if i == rows {
			for _, v := range colLeft {
				if v != 0 {
					return false
				}
			}
			return true
		}
		if j == cols {
			if rowLeft[i] != 0 {
				return false
			}
			return rec(i+1, 0)
		}
		if spec.Forbidden[Cell{Row: i, Col: j}] {
			return rec(i, j+1)
		}
		hi := rowLeft[i]
		if colLeft[j] < hi {
			hi = colLeft[j]
		}
		for v := 0; v <= hi; v++ {
			rowLeft[i] -= v
			colLeft[j] -= v
			ok := rec(i, j+1)
			rowLeft[i] += v
			colLeft[j] += v
			if ok {
				return true
			}
		}
		return false
	}
	return rec(0, 0)
}

func TestEnumerationAgreement(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 2000; trial++ {
		spec := randSpec(rng, 3, 6)
		tab, err := Solve(spec)
		feasible := bruteFeasible(spec)
		if feasible && err != nil {
			t.Fatalf("trial %d: solver reports infeasible (%v) but enumeration found a table for %+v", trial, err, spec)
		}
		if !feasible && err == nil {
			t.Fatalf("trial %d: solver found %v but enumeration proves infeasible for %+v", trial, tab, spec)
		}
		if feasible {
			checkTable(t, spec, tab)
		} else {
			checkCertificate(t, spec, err.(*Evidence))
		}
	}
}

func TestGenerateShufflePreservesQuotas(t *testing.T) {
	spec := Spec{
		RowTotals: []int{10, 20, 5},
		ColTotals: []int{15, 15, 5},
		Forbidden: map[Cell]bool{{Row: 0, Col: 2}: true, {Row: 2, Col: 0}: true},
	}
	base, err := Generate(spec, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(base) != 35 {
		t.Fatalf("got %d records, want 35", len(base))
	}
	want := Counts(base, 3, 3)
	seenPermuted := false
	for seed := int64(0); seed < 20; seed++ {
		recs, err := Generate(spec, rand.New(rand.NewSource(seed)))
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		got := Counts(recs, 3, 3)
		for i := range want {
			for j := range want[i] {
				if got[i][j] != want[i][j] {
					t.Fatalf("seed %d: quota at (%d,%d) changed: %d != %d", seed, i, j, got[i][j], want[i][j])
				}
			}
		}
		for k := range recs {
			if recs[k] != base[k] {
				seenPermuted = true
				break
			}
		}
	}
	if !seenPermuted {
		t.Fatal("shuffle never changed record order across 20 seeds")
	}
}

func TestKnownInfeasible(t *testing.T) {
	spec := Spec{
		RowTotals: []int{3},
		ColTotals: []int{1, 1, 1},
		Forbidden: map[Cell]bool{{Row: 0, Col: 2}: true},
	}
	_, err := Solve(spec)
	ev, ok := err.(*Evidence)
	if !ok {
		t.Fatalf("expected *Evidence, got %v", err)
	}
	checkCertificate(t, spec, ev)
	if ev.Demand != 3 || ev.Supply != 2 {
		t.Fatalf("got demand %d supply %d, want 3 and 2", ev.Demand, ev.Supply)
	}
}

func TestLimitsAndValidation(t *testing.T) {
	if _, err := Solve(Spec{RowTotals: []int{60}, ColTotals: []int{60}}); err != nil {
		t.Fatalf("total 60 within limit: %v", err)
	}
	if _, err := Solve(Spec{RowTotals: []int{101}, ColTotals: []int{101}}); err != ErrTotalTooLarge {
		t.Fatalf("total 101: got %v, want ErrTotalTooLarge", err)
	}
	if _, err := Solve(Spec{RowTotals: []int{2}, ColTotals: []int{3}}); err == nil {
		t.Fatal("mismatched marginal sums should fail")
	} else if ev, ok := err.(*Evidence); !ok || ev.Demand != 2 || ev.Supply != 3 {
		t.Fatalf("mismatched sums: got %v", err)
	}
	if _, err := Solve(Spec{RowTotals: []int{-1}, ColTotals: []int{-1}}); err == nil {
		t.Fatal("negative totals should fail")
	}
	if _, err := Solve(Spec{
		RowTotals: []int{1},
		ColTotals: []int{1},
		Forbidden: map[Cell]bool{{Row: 0, Col: 5}: true},
	}); err == nil {
		t.Fatal("out-of-range forbidden cell should fail")
	}
}

func TestEmptySpec(t *testing.T) {
	tab, err := Solve(Spec{})
	if err != nil {
		t.Fatalf("empty spec: %v", err)
	}
	if len(tab) != 0 {
		t.Fatalf("empty spec: got %v", tab)
	}
	recs, err := Generate(Spec{}, rand.New(rand.NewSource(7)))
	if err != nil || len(recs) != 0 {
		t.Fatalf("empty generate: %v, %v", recs, err)
	}
}
