package metrics

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestNewFromSamples(t *testing.T) {
	h := NewFromSamples([]float64{3, 1, 2, 2}, []float64{0.5, 1, 1.5, 2.5, 9})
	wantBounds := []float64{1, 2, 3}
	wantCum := []uint64{2, 3, 4}
	if h.Total != 5 {
		t.Fatalf("Total = %d, want 5", h.Total)
	}
	for i := range wantBounds {
		if h.Bounds[i] != wantBounds[i] || h.Cum[i] != wantCum[i] {
			t.Fatalf("bucket %d = (%v, %d), want (%v, %d)", i, h.Bounds[i], h.Cum[i], wantBounds[i], wantCum[i])
		}
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestMergeIdenticalBounds(t *testing.T) {
	a := NewFromSamples([]float64{1, 2, 3}, []float64{0.5, 1.5, 2.5})
	b := NewFromSamples([]float64{1, 2, 3}, []float64{0.1, 0.2, 10})
	m := Merge(a, b)
	want := NewFromSamples([]float64{1, 2, 3}, []float64{0.5, 1.5, 2.5, 0.1, 0.2, 10})
	if !m.Equals(want) {
		t.Fatalf("merge = %+v, want %+v", m, want)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestMergeProjectsToCommonBounds verifies that merging differing boundary
// sets projects onto the intersection, and that every projected cumulative
// count equals the count obtained from the combined raw samples.
func TestMergeProjectsToCommonBounds(t *testing.T) {
	s1 := []float64{0.2, 0.7, 1.2, 1.7, 2.2, 2.7, 3.5, 8}
	s2 := []float64{0.1, 0.9, 1.5, 2.5, 4.5}
	a := NewFromSamples([]float64{0.5, 1, 2, 3}, s1)
	b := NewFromSamples([]float64{1, 2, 4, 5}, s2)

	m := Merge(a, b)
	wantBounds := []float64{1, 2}
	if len(m.Bounds) != len(wantBounds) {
		t.Fatalf("merged bounds = %v, want %v", m.Bounds, wantBounds)
	}
	for i, le := range wantBounds {
		if m.Bounds[i] != le {
			t.Fatalf("merged bounds = %v, want %v", m.Bounds, wantBounds)
		}
	}

	raw := NewFromSamples(wantBounds, append(append([]float64{}, s1...), s2...))
	if !m.Equals(raw) {
		t.Fatalf("merged %+v != raw-sample histogram %+v", m, raw)
	}
	if m.Total != uint64(len(s1)+len(s2)) {
		t.Fatalf("merged total = %d, want %d", m.Total, len(s1)+len(s2))
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// No fabricated counts at non-common bounds.
	if _, ok := m.CumAt(0.5); ok {
		t.Fatal("merged histogram claims a count at non-common bound 0.5")
	}
	if _, ok := m.CumAt(4); ok {
		t.Fatal("merged histogram claims a count at non-common bound 4")
	}
}

// TestMergeEmptyIntersection verifies that disjoint boundary sets merge into
// a histogram carrying only the total count.
func TestMergeEmptyIntersection(t *testing.T) {
	a := NewFromSamples([]float64{1, 2}, []float64{0.5, 1.5, 2.5})
	b := NewFromSamples([]float64{10, 20}, []float64{5, 15})
	m := Merge(a, b)
	if len(m.Bounds) != 0 || len(m.Cum) != 0 {
		t.Fatalf("expected empty projection, got bounds %v", m.Bounds)
	}
	if m.Total != 5 {
		t.Fatalf("Total = %d, want 5", m.Total)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate(t *testing.T) {
	good := &Histogram{Bounds: []float64{1, 2}, Cum: []uint64{2, 5}, Total: 6}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid histogram rejected: %v", err)
	}
	// Final cumulative count equals total exactly when +Inf adds nothing.
	exact := &Histogram{Bounds: []float64{1, 2}, Cum: []uint64{2, 6}, Total: 6}
	if err := exact.Validate(); err != nil {
		t.Fatalf("valid histogram rejected: %v", err)
	}
	cases := map[string]*Histogram{
		"length mismatch":  {Bounds: []float64{1}, Cum: []uint64{1, 2}, Total: 2},
		"non-monotonic":    {Bounds: []float64{1, 2}, Cum: []uint64{3, 2}, Total: 5},
		"unsorted bounds":  {Bounds: []float64{2, 1}, Cum: []uint64{1, 2}, Total: 5},
		"cum above total":  {Bounds: []float64{1}, Cum: []uint64{6}, Total: 5},
		"non-finite bound": {Bounds: []float64{math.Inf(1)}, Cum: []uint64{1}, Total: 1},
	}
	for name, h := range cases {
		if err := h.Validate(); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

// TestUndoBatchRecomputesFromRawSamples adds batches, undoes one, and checks
// the registry histogram against one built directly from the remaining raw
// samples.
func TestUndoBatchRecomputesFromRawSamples(t *testing.T) {
	bounds := []float64{1, 2, 3}
	r := NewRegistry(bounds)
	b1 := []float64{0.5, 1.5, 2.5}
	b2 := []float64{0.1, 0.2, 10}
	b3 := []float64{2.9}
	for id, s := range map[string][]float64{"b1": b1, "b2": b2, "b3": b3} {
		if err := r.AddBatch(id, s); err != nil {
			t.Fatalf("AddBatch(%s): %v", id, err)
		}
	}
	if err := r.AddBatch("b1", b1); err == nil {
		t.Fatal("duplicate AddBatch accepted")
	}

	if got, want := r.Histogram(), NewFromSamples(bounds, append(append(append([]float64{}, b1...), b2...), b3...)); !got.Equals(want) {
		t.Fatalf("before undo: got %+v, want %+v", got, want)
	}

	if !r.UndoBatch("b2") {
		t.Fatal("UndoBatch(b2) = false")
	}
	if r.UndoBatch("b2") {
		t.Fatal("second UndoBatch(b2) = true")
	}
	if got, want := r.Histogram(), NewFromSamples(bounds, append(append([]float64{}, b1...), b3...)); !got.Equals(want) {
		t.Fatalf("after undo: got %+v, want %+v", got, want)
	}
	if err := r.Histogram().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestMergeRandomizedAgainstRawSamples fuzzes merging against histograms
// built from combined raw samples over the common bounds.
func TestMergeRandomizedAgainstRawSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		b1 := randomBounds(rng)
		b2 := randomBounds(rng)
		s1 := randomSamples(rng, rng.Intn(50))
		s2 := randomSamples(rng, rng.Intn(50))

		m := Merge(NewFromSamples(b1, s1), NewFromSamples(b2, s2))
		if err := m.Validate(); err != nil {
			t.Fatalf("trial %d: invalid merge: %v", trial, err)
		}
		if m.Total != uint64(len(s1)+len(s2)) {
			t.Fatalf("trial %d: total = %d, want %d", trial, m.Total, len(s1)+len(s2))
		}
		// Merged bounds must be exactly the intersection.
		common := intersectBounds(normalizeBounds(b1), normalizeBounds(b2))
		if len(m.Bounds) != len(common) {
			t.Fatalf("trial %d: bounds %v, want intersection %v", trial, m.Bounds, common)
		}
		for i := range common {
			if m.Bounds[i] != common[i] {
				t.Fatalf("trial %d: bounds %v, want intersection %v", trial, m.Bounds, common)
			}
		}
		// Every merged count must match the raw-sample count.
		all := append(append([]float64{}, s1...), s2...)
		raw := NewFromSamples(common, all)
		if !m.Equals(raw) {
			t.Fatalf("trial %d: merged %+v != raw %+v", trial, m, raw)
		}
	}
}

func randomBounds(rng *rand.Rand) []float64 {
	n := rng.Intn(6)
	set := map[float64]bool{}
	var out []float64
	for len(out) < n {
		le := float64(rng.Intn(10))
		if !set[le] {
			set[le] = true
			out = append(out, le)
		}
	}
	sort.Float64s(out)
	return out
}

func randomSamples(rng *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = rng.Float64() * 12
	}
	return out
}
