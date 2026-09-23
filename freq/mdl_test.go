package freq

import (
	"math"
	"testing"
)

// TestHeaderCostMatters checks that the model header cost is really
// part of the decision: for i.i.d. data the cheap order-0 structure
// (all contexts merged) must win, while for strongly context-dependent
// data the fully split structure must win.
func TestHeaderCostMatters(t *testing.T) {
	const nctx, alpha = 4, 2

	// i.i.d. data: context carries no information.
	iid := make([][]uint64, nctx)
	for c := range iid {
		iid[c] = []uint64{500, 500}
	}
	best, modelBits, _ := SelectExhaustive(iid, alpha, DefaultK)
	groups := 0
	seen := map[int]bool{}
	for _, g := range best {
		if !seen[g] {
			seen[g] = true
			groups++
		}
	}
	if groups != 1 {
		t.Fatalf("i.i.d. data: best structure has %d groups, want 1 (header cost should kill split models)", groups)
	}
	// Sanity: the single-group structure pays no assignment bits.
	if modelBits != HeaderFixedBits+alpha*ParamBitsPerCount {
		t.Fatalf("order-0 model cost = %v, want %v", modelBits, float64(HeaderFixedBits+alpha*ParamBitsPerCount))
	}

	// Deterministic-per-context data with a distinct distribution per
	// context: context fully predicts the symbol, and no two contexts
	// can be merged without loss. Use alpha == nctx so each context can
	// favour its own symbol.
	det := make([][]uint64, nctx)
	for c := range det {
		det[c] = make([]uint64, nctx)
		det[c][c] = 500
	}
	best2, _, dataBits := SelectExhaustive(det, nctx, DefaultK)
	seen2 := map[int]bool{}
	for _, g := range best2 {
		seen2[g] = true
	}
	if len(seen2) != nctx {
		t.Fatalf("deterministic data: best structure has %d groups, want %d (fully split)", len(seen2), nctx)
	}
	// Data cost is not exactly 0 because smoothing assigns a tiny
	// probability to the unseen symbol, but it must be small relative
	// to the i.i.d. alternative (~1 bit/symbol).
	if dataBits > 0.05*float64(nctx*500) {
		t.Fatalf("deterministic data cost = %v bits, suspiciously high", dataBits)
	}
}

// TestExhaustiveConsistentWithRecompute verifies that statistics
// accumulated incrementally (with retractions) give the same MDL
// decision as a full recompute from the surviving records.
func TestExhaustiveConsistentWithRecompute(t *testing.T) {
	const nctx, alpha = 4, 3
	rng := uint64(424242)
	next := func(n uint64) uint64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return (rng >> 33) % n
	}
	type rec struct{ ctx, sym byte }
	inc := make([][]uint64, nctx)
	for i := range inc {
		inc[i] = make([]uint64, alpha)
	}
	var live []rec
	for step := 0; step < 3000; step++ {
		if len(live) == 0 || next(3) != 0 {
			r := rec{byte(next(nctx)), byte(next(alpha))}
			inc[r.ctx][r.sym]++
			live = append(live, r)
		} else {
			i := int(next(uint64(len(live))))
			inc[live[i].ctx][live[i].sym]--
			live = append(live[:i], live[i+1:]...)
		}
	}
	var ctxs, syms []byte
	for _, r := range live {
		ctxs = append(ctxs, r.ctx)
		syms = append(syms, r.sym)
	}
	full := HistFromRecords(ctxs, syms, nctx, alpha)
	for c := 0; c < nctx; c++ {
		for s := 0; s < alpha; s++ {
			if inc[c][s] != full[c][s] {
				t.Fatalf("hist[%d][%d]: incremental %d != recompute %d", c, s, inc[c][s], full[c][s])
			}
		}
	}
	b1, m1, d1 := SelectExhaustive(inc, alpha, DefaultK)
	b2, m2, d2 := SelectExhaustive(full, alpha, DefaultK)
	if m1 != m2 || d1 != d2 {
		t.Fatalf("MDL costs differ: incremental (%v,%v) vs recompute (%v,%v)", m1, d1, m2, d2)
	}
	if len(b1) != len(b2) {
		t.Fatalf("selected structures differ: %v vs %v", b1, b2)
	}
}

// TestZeroFrequencyNoLog0 exercises StructureCost with empty groups:
// the data cost must be finite and zero-frequency symbols must add 0.
func TestZeroFrequencyNoLog0(t *testing.T) {
	hist := [][]uint64{
		{10, 0, 0},
		{0, 0, 0}, // empty context
	}
	assign := []int{0, 0}
	_, data := StructureCost(hist, assign, 3, DefaultK)
	if math.IsNaN(data) || math.IsInf(data, 0) {
		t.Fatalf("data cost = %v, want finite", data)
	}
	// Only symbol 0 occurs, 10 times, with add-k smoothing over alpha=3.
	p := (10.0 + DefaultK) / (10.0 + DefaultK*3)
	want := 10.0 * -math.Log2(p)
	if math.Abs(data-want) > 1e-9 {
		t.Fatalf("data cost = %v, want %v", data, want)
	}
}

// TestSelectExhaustiveCoversExtremes: the exhaustive search must never
// do worse than either extreme (fully merged / fully split).
func TestSelectExhaustiveCoversExtremes(t *testing.T) {
	const nctx, alpha = 5, 3
	rng := uint64(7)
	next := func(n uint64) uint64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return (rng >> 33) % n
	}
	hist := make([][]uint64, nctx)
	for c := range hist {
		hist[c] = make([]uint64, alpha)
		for i := 0; i < 200; i++ {
			hist[c][next(alpha)]++
		}
	}
	best, bm, bd := SelectExhaustive(hist, alpha, DefaultK)
	_ = best
	bestCost := bm + bd
	merged := make([]int, nctx) // all group 0
	split := make([]int, nctx)
	for i := range split {
		split[i] = i
	}
	for name, assign := range map[string][]int{"merged": merged, "split": split} {
		m, d := StructureCost(hist, assign, alpha, DefaultK)
		if bestCost > m+d+1e-9 {
			t.Fatalf("exhaustive cost %v worse than %s structure %v", bestCost, name, m+d)
		}
	}
}
