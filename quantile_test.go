package stats

import (
	"math/rand"
	"sort"
	"testing"
)

// expand turns weighted samples into the raw sequence each weight-weighted
// sample stands for, serving as the reference model.
func expand(ops []Op) []int64 {
	var out []int64
	for _, op := range ops {
		if op.Remove {
			panic("expand model takes adds only")
		}
		for i := uint64(0); i < op.Weight; i++ {
			out = append(out, op.Value)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// modelQuantile applies the quantile definition directly to the expanded
// sequence: the smallest value whose cumulative count reaches q*len, with
// q <= 0 mapped to the minimum.
func modelQuantile(samples []int64, q float64) (int64, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	if q <= 0 {
		return samples[0], true
	}
	if q > 1 {
		q = 1
	}
	threshold := q * float64(len(samples))
	for i, v := range samples {
		if float64(i+1) >= threshold {
			return v, true
		}
	}
	return samples[len(samples)-1], true
}

func applyOps(t *testing.T, w *WeightedQuantile, ops []Op) {
	t.Helper()
	for _, op := range ops {
		if op.Remove {
			if err := w.Remove(op.Value, op.Weight); err != nil {
				t.Fatalf("Remove(%d, %d): %v", op.Value, op.Weight, err)
			}
		} else {
			w.Add(op.Value, op.Weight)
		}
	}
}

func checkAgainstModel(t *testing.T, w *WeightedQuantile, samples []int64) {
	t.Helper()
	for _, q := range []float64{-1, 0, 0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 1, 2} {
		got, gotOK := w.Quantile(q)
		want, wantOK := modelQuantile(samples, q)
		if gotOK != wantOK || got != want {
			t.Fatalf("q=%v: got (%d, %v), want (%d, %v)", q, got, gotOK, want, wantOK)
		}
	}
}

func TestQuantileEmptySetIsSeparate(t *testing.T) {
	w := NewWeightedQuantile()
	if _, ok := w.Quantile(0.5); ok {
		t.Fatal("empty set must report ok=false")
	}
	w.Add(7, 0) // zero weight keeps the set empty
	if _, ok := w.Quantile(0.5); ok {
		t.Fatal("zero-weight-only set must report ok=false")
	}
	w.Add(7, 2)
	if _, ok := w.Quantile(0.5); !ok {
		t.Fatal("non-empty set must report ok=true")
	}
	if err := w.Remove(7, 2); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Quantile(0.5); ok {
		t.Fatal("set emptied by removals must report ok=false")
	}
}

func TestQuantileEndpoints(t *testing.T) {
	ops := []Op{
		{Value: 1, Weight: 0}, // zero-weight below the minimum
		{Value: 3, Weight: 2},
		{Value: 5, Weight: 3},
		{Value: 9, Weight: 1},
		{Value: 12, Weight: 0}, // zero-weight above the maximum
	}
	w := NewWeightedQuantile()
	applyOps(t, w, ops)
	checkAgainstModel(t, w, expand(ops))

	if got, _ := w.Quantile(0); got != 3 {
		t.Fatalf("q=0: got %d, want 3 (smallest positive-weight value)", got)
	}
	if got, _ := w.Quantile(1); got != 9 {
		t.Fatalf("q=1: got %d, want 9", got)
	}
}

func TestQuantileDuplicateValues(t *testing.T) {
	ops := []Op{
		{Value: 4, Weight: 1},
		{Value: 4, Weight: 2}, // duplicate value adds up
		{Value: 4, Weight: 0},
		{Value: 2, Weight: 1},
		{Value: 8, Weight: 1},
	}
	w := NewWeightedQuantile()
	applyOps(t, w, ops)
	if w.Len() != 3 {
		t.Fatalf("distinct values: got %d, want 3", w.Len())
	}
	if w.Total() != 5 {
		t.Fatalf("total weight: got %d, want 5", w.Total())
	}
	checkAgainstModel(t, w, expand(ops))
}

func TestQuantileZeroWeightsNeverSurface(t *testing.T) {
	w := NewWeightedQuantile()
	w.Add(-100, 0)
	w.Add(100, 0)
	w.Add(5, 1)
	for _, q := range []float64{0, 0.0001, 0.5, 1} {
		if got, ok := w.Quantile(q); !ok || got != 5 {
			t.Fatalf("q=%v: got (%d, %v), want (5, true)", q, got, ok)
		}
	}
}

func TestRemoveUnderflow(t *testing.T) {
	w := NewWeightedQuantile()
	w.Add(3, 1)
	if err := w.Remove(3, 2); err == nil {
		t.Fatal("expected underflow error")
	}
	if err := w.Remove(9, 1); err == nil {
		t.Fatal("expected underflow error for absent value")
	}
	if got, ok := w.Quantile(0.5); !ok || got != 3 {
		t.Fatalf("failed removes must not change the set: got (%d, %v)", got, ok)
	}
	if err := w.Remove(3, 0); err != nil {
		t.Fatalf("zero-weight remove must be a no-op: %v", err)
	}
}

// TestQuantileRandomExpanded cross-checks random online add/remove sequences
// against the expanded-sequence model after every step.
func TestQuantileRandomExpanded(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		w := NewWeightedQuantile()
		var samples []int64
		steps := 1 + rng.Intn(40)
		for step := 0; step < steps; step++ {
			if len(samples) > 0 && rng.Intn(3) == 0 {
				// remove a random in-flight value with a random valid weight
				idx := rng.Intn(len(samples))
				v := samples[idx]
				have := 0
				for _, s := range samples {
					if s == v {
						have++
					}
				}
				weight := uint64(1 + rng.Intn(have))
				if err := w.Remove(v, weight); err != nil {
					t.Fatalf("trial %d: %v", trial, err)
				}
				left := int(weight)
				next := samples[:0]
				for _, s := range samples {
					if s == v && left > 0 {
						left--
						continue
					}
					next = append(next, s)
				}
				samples = next
			} else {
				v := int64(rng.Intn(11) - 5)
				weight := uint64(rng.Intn(4)) // includes zero weights
				w.Add(v, weight)
				for i := uint64(0); i < weight; i++ {
					samples = append(samples, v)
				}
			}
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			checkAgainstModel(t, w, samples)
		}
	}
}
