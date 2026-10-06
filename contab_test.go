package contab

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestSolveConservation(t *testing.T) {
	spec := Spec{
		RowCounts: []int{15, 25, 10},
		ColCounts: []int{20, 20, 10},
		Forbidden: []Cell{{0, 2}, {2, 0}},
	}
	table, ev, err := Solve(spec)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if ev != nil {
		t.Fatalf("unexpected infeasibility: %v", ev)
	}
	if err := Verify(spec, table); err != nil {
		t.Fatalf("conservation violated: %v", err)
	}
}

func TestSolveStructuralZeros(t *testing.T) {
	// Row 0 may only use column 1; column 1's whole margin must come from it.
	spec := Spec{
		RowCounts: []int{4, 6},
		ColCounts: []int{6, 4},
		Forbidden: []Cell{{0, 0}},
	}
	table, ev, err := Solve(spec)
	if err != nil || ev != nil {
		t.Fatalf("Solve: err=%v ev=%v", err, ev)
	}
	want := [][]int{{0, 4}, {6, 0}}
	if !reflect.DeepEqual(table, want) {
		t.Fatalf("got %v, want %v", table, want)
	}
}

func TestSolveTotalsMismatch(t *testing.T) {
	spec := Spec{RowCounts: []int{3}, ColCounts: []int{2, 2}}
	_, ev, err := Solve(spec)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if ev == nil || ev.Rows != nil || ev.Demand != 3 || ev.Supply != 4 {
		t.Fatalf("bad evidence: %+v", ev)
	}
}

func TestSolveInfeasibleEvidence(t *testing.T) {
	// Rows 0 and 1 (demand 8) may only reach column 0 (supply 5).
	spec := Spec{
		RowCounts: []int{5, 3, 2},
		ColCounts: []int{5, 5},
		Forbidden: []Cell{{0, 1}, {1, 1}},
	}
	table, ev, err := Solve(spec)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if table != nil {
		t.Fatalf("expected no table, got %v", table)
	}
	if ev == nil {
		t.Fatal("expected evidence")
	}
	checkEvidence(t, spec, ev)
	if got := ev.SortedEvidenceRows(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("evidence rows = %v, want [0 1]", got)
	}
}

// checkEvidence independently validates a certificate: the listed rows must
// genuinely demand more than the columns they are allowed to use can supply.
func checkEvidence(t *testing.T, spec Spec, ev *Evidence) {
	t.Helper()
	if ev.Rows == nil {
		rt, ct := 0, 0
		for _, c := range spec.RowCounts {
			rt += c
		}
		for _, c := range spec.ColCounts {
			ct += c
		}
		if rt == ct {
			t.Fatalf("totals-match evidence but totals are equal (%d)", rt)
		}
		return
	}
	forbidden := map[Cell]bool{}
	for _, f := range spec.Forbidden {
		forbidden[f] = true
	}
	demand := 0
	for _, i := range ev.Rows {
		demand += spec.RowCounts[i]
	}
	supply := 0
	for j := range spec.ColCounts {
		usable := false
		for _, i := range ev.Rows {
			if !forbidden[Cell{i, j}] {
				usable = true
				break
			}
		}
		if usable {
			supply += spec.ColCounts[j]
		}
	}
	if demand != ev.Demand || supply != ev.Supply {
		t.Fatalf("evidence numbers wrong: got (%d,%d), recomputed (%d,%d)",
			ev.Demand, ev.Supply, demand, supply)
	}
	if demand <= supply {
		t.Fatalf("evidence does not prove infeasibility: demand %d <= supply %d", demand, supply)
	}
}

// enumerateFeasible brute-forces every non-negative table meeting the margins
// and reports whether one respects the forbidden cells. Ground truth for
// small instances.
func enumerateFeasible(spec Spec) bool {
	nr, nc := len(spec.RowCounts), len(spec.ColCounts)
	forbidden := map[Cell]bool{}
	for _, f := range spec.Forbidden {
		forbidden[f] = true
	}
	rowRem := append([]int(nil), spec.RowCounts...)
	colRem := append([]int(nil), spec.ColCounts...)
	var rec func(cell int) bool
	rec = func(cell int) bool {
		if cell == nr*nc {
			for _, r := range rowRem {
				if r != 0 {
					return false
				}
			}
			for _, c := range colRem {
				if c != 0 {
					return false
				}
			}
			return true
		}
		i, j := cell/nc, cell%nc
		if forbidden[Cell{i, j}] {
			return rec(cell + 1)
		}
		for v := 0; v <= min(rowRem[i], colRem[j]); v++ {
			rowRem[i] -= v
			colRem[j] -= v
			if rec(cell + 1) {
				return true
			}
			rowRem[i] += v
			colRem[j] += v
		}
		return false
	}
	return rec(0)
}

func TestSolveMatchesEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		nr := 2 + rng.Intn(2) // 2..3 rows
		nc := 2 + rng.Intn(2) // 2..3 cols
		total := rng.Intn(7)  // 0..6 records
		rows := randomComposition(rng, total, nr)
		cols := randomComposition(rng, total, nc)
		var forbidden []Cell
		for i := 0; i < nr; i++ {
			for j := 0; j < nc; j++ {
				if rng.Intn(3) == 0 {
					forbidden = append(forbidden, Cell{i, j})
				}
			}
		}
		spec := Spec{RowCounts: rows, ColCounts: cols, Forbidden: forbidden}
		table, ev, err := Solve(spec)
		if err != nil {
			t.Fatalf("trial %d spec %+v: %v", trial, spec, err)
		}
		want := enumerateFeasible(spec)
		if (ev == nil) != want {
			t.Fatalf("trial %d spec %+v: Solve feasible=%v, enumeration=%v (ev=%v)",
				trial, spec, ev == nil, want, ev)
		}
		if ev == nil {
			if err := Verify(spec, table); err != nil {
				t.Fatalf("trial %d spec %+v: %v", trial, spec, err)
			}
		} else {
			checkEvidence(t, spec, ev)
		}
	}
}

// randomComposition splits total into n non-negative parts uniformly enough
// for testing.
func randomComposition(rng *rand.Rand, total, n int) []int {
	cuts := map[int]bool{}
	for len(cuts) < n-1 {
		cuts[rng.Intn(total+n-1)] = true
	}
	stars := make([]int, 0, n-1)
	for c := range cuts {
		stars = append(stars, c)
	}
	sort.Ints(stars)
	parts := make([]int, n)
	prev := -1
	for k, s := range stars {
		parts[k] = s - prev - 1
		prev = s
	}
	parts[n-1] = total + n - 2 - prev
	return parts
}

func TestGenerateQuotasSurviveShuffle(t *testing.T) {
	spec := Spec{
		RowCounts: []int{30, 45, 25},
		ColCounts: []int{40, 35, 25},
		Forbidden: []Cell{{0, 2}, {1, 0}, {2, 2}},
	}
	base, ev, err := Generate(spec, false, nil)
	if err != nil || ev != nil {
		t.Fatalf("Generate: err=%v ev=%v", err, ev)
	}
	if len(base) != 100 {
		t.Fatalf("got %d records, want 100", len(base))
	}
	baseQuotas, err := Quotas(spec, base)
	if err != nil {
		t.Fatalf("Quotas: %v", err)
	}
	if err := Verify(spec, baseQuotas); err != nil {
		t.Fatalf("unshuffled quotas violate spec: %v", err)
	}

	for seed := int64(1); seed <= 5; seed++ {
		rng := rand.New(rand.NewSource(seed))
		shuffled, ev, err := Generate(spec, true, rng)
		if err != nil || ev != nil {
			t.Fatalf("Generate seed %d: err=%v ev=%v", seed, err, ev)
		}
		quotas, err := Quotas(spec, shuffled)
		if err != nil {
			t.Fatalf("Quotas seed %d: %v", seed, err)
		}
		if !reflect.DeepEqual(quotas, baseQuotas) {
			t.Fatalf("seed %d changed quotas: got %v, want %v", seed, quotas, baseQuotas)
		}
		if err := Verify(spec, quotas); err != nil {
			t.Fatalf("seed %d quotas violate spec: %v", seed, err)
		}
	}
}

func TestGenerateNilRngReproducible(t *testing.T) {
	spec := Spec{RowCounts: []int{3, 5}, ColCounts: []int{4, 4}}
	a, _, _ := Generate(spec, true, nil)
	b, _, _ := Generate(spec, true, nil)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("nil rng not reproducible: %v vs %v", a, b)
	}
}

func TestGenerateInfeasibleReturnsEvidence(t *testing.T) {
	spec := Spec{
		RowCounts: []int{10},
		ColCounts: []int{4, 6},
		Forbidden: []Cell{{0, 1}},
	}
	recs, ev, err := Generate(spec, true, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if recs != nil || ev == nil {
		t.Fatalf("got recs=%v ev=%v", recs, ev)
	}
	checkEvidence(t, spec, ev)
}

func TestSolveHundredRecords(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		nr := 2 + rng.Intn(5)
		nc := 2 + rng.Intn(5)
		spec := Spec{
			RowCounts: randomComposition(rng, 100, nr),
			ColCounts: randomComposition(rng, 100, nc),
		}
		for i := 0; i < nr; i++ {
			for j := 0; j < nc; j++ {
				if rng.Intn(4) == 0 {
					spec.Forbidden = append(spec.Forbidden, Cell{i, j})
				}
			}
		}
		table, ev, err := Solve(spec)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if ev != nil {
			checkEvidence(t, spec, ev)
			continue
		}
		if err := Verify(spec, table); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []Spec{
		{RowCounts: []int{-1}, ColCounts: []int{0}},
		{RowCounts: []int{1}, ColCounts: []int{1}, Forbidden: []Cell{{1, 0}}},
		{RowCounts: []int{1}, ColCounts: []int{1}, Forbidden: []Cell{{0, 0}, {0, 0}}},
		{RowCounts: nil, ColCounts: []int{1}},
	}
	for i, spec := range cases {
		if _, _, err := Solve(spec); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}
