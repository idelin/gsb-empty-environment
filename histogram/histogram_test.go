package histogram

import (
	"reflect"
	"testing"
)

// countLE counts raw samples <= boundary, the ground truth for any
// cumulative bucket.
func countLE(samples []float64, boundary float64) uint64 {
	var n uint64
	for _, s := range samples {
		if s <= boundary {
			n++
		}
	}
	return n
}

func assertValid(t *testing.T, h *Histogram) {
	t.Helper()
	if err := h.Validate(); err != nil {
		t.Fatalf("invalid histogram: %v", err)
	}
}

func TestNewMatchesRawSamples(t *testing.T) {
	boundaries := []float64{10, 1, 5, 5} // unsorted, duplicated on purpose
	samples := []float64{0.5, 1, 3, 7, 10, 42}
	h := New(boundaries, samples)
	assertValid(t, h)

	wantBoundaries := []float64{1, 5, 10}
	if !reflect.DeepEqual(h.Boundaries, wantBoundaries) {
		t.Fatalf("boundaries = %v, want %v", h.Boundaries, wantBoundaries)
	}
	for i, b := range h.Boundaries {
		if got, want := h.Cumulative[i], countLE(samples, b); got != want {
			t.Errorf("cumulative at %v = %d, want %d", b, got, want)
		}
	}
	if h.Cumulative[len(h.Cumulative)-1] != uint64(len(samples)) {
		t.Errorf("final cumulative = %d, want %d", h.Cumulative[len(h.Cumulative)-1], len(samples))
	}
}

func TestMergeProjectsOntoCommonBoundaries(t *testing.T) {
	samplesA := []float64{0.5, 1.5, 3, 8, 30}
	samplesB := []float64{2, 4, 5, 6, 100}
	a := New([]float64{1, 2, 5, 10}, samplesA)
	b := New([]float64{2, 5, 20}, samplesB)

	merged := Merge(a, b)
	assertValid(t, merged)

	// Only boundaries present in BOTH inputs survive.
	wantBoundaries := []float64{2, 5}
	if !reflect.DeepEqual(merged.Boundaries, wantBoundaries) {
		t.Fatalf("merged boundaries = %v, want %v", merged.Boundaries, wantBoundaries)
	}

	// Every merged bucket must equal the exact count over the combined
	// raw samples — no interpolated/fabricated counts.
	all := append(append([]float64{}, samplesA...), samplesB...)
	for i, boundary := range merged.Boundaries {
		if got, want := merged.Cumulative[i], countLE(all, boundary); got != want {
			t.Errorf("merged cumulative at %v = %d, want exact %d", boundary, got, want)
		}
	}
	if merged.Total != uint64(len(all)) {
		t.Errorf("merged total = %d, want %d", merged.Total, len(all))
	}
}

func TestMergeEmptyIntersectionKeepsOnlyTotal(t *testing.T) {
	a := New([]float64{1, 2, 3}, []float64{0.5, 2.5})
	b := New([]float64{10, 20}, []float64{15, 25, 30})

	merged := Merge(a, b)
	assertValid(t, merged)

	if len(merged.Boundaries) != 0 {
		t.Fatalf("merged boundaries = %v, want none", merged.Boundaries)
	}
	if !reflect.DeepEqual(merged.Cumulative, []uint64{5}) {
		t.Fatalf("merged cumulative = %v, want [5]", merged.Cumulative)
	}
	if merged.Total != 5 {
		t.Fatalf("merged total = %d, want 5", merged.Total)
	}
}

func TestProjectRejectsUnknownBoundary(t *testing.T) {
	h := New([]float64{1, 5}, []float64{1, 2, 3})
	if _, err := h.Project([]float64{1, 3}); err == nil {
		t.Fatal("Project onto missing boundary succeeded; want interpolation refusal")
	}
	p, err := h.Project([]float64{5})
	if err != nil {
		t.Fatalf("Project onto existing boundary failed: %v", err)
	}
	if !reflect.DeepEqual(p.Cumulative, []uint64{3, 3}) {
		t.Fatalf("projected cumulative = %v, want [3 3]", p.Cumulative)
	}
}

func TestUndoBatchRecomputesFromRawSamples(t *testing.T) {
	r := NewRecorder([]float64{1, 2, 5, 10})
	batch1 := []float64{0.5, 1.5, 3}
	batch2 := []float64{2, 4, 20}
	batch3 := []float64{7, 8}
	id1 := r.RecordBatch(batch1)
	id2 := r.RecordBatch(batch2)
	id3 := r.RecordBatch(batch3)

	if !r.Undo(id2) {
		t.Fatal("Undo of existing batch returned false")
	}
	if r.Undo(id2) {
		t.Fatal("Undo of already-removed batch returned true")
	}

	got := r.Histogram()
	assertValid(t, got)

	// After undo, the histogram must equal one built directly from the
	// raw samples of the remaining batches.
	remaining := append(append([]float64{}, batch1...), batch3...)
	want := New([]float64{1, 2, 5, 10}, remaining)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after undo got %+v, want %+v", got, want)
	}

	// Undoing everything leaves an empty (but valid) histogram.
	r.Undo(id1)
	r.Undo(id3)
	empty := r.Histogram()
	assertValid(t, empty)
	if empty.Total != 0 {
		t.Fatalf("empty histogram total = %d, want 0", empty.Total)
	}
}

func TestMergeAfterUndoMatchesRawRebuild(t *testing.T) {
	// Two recorders with different boundary sets; undo a batch in one,
	// then merge and check against raw samples on common boundaries.
	ra := NewRecorder([]float64{1, 2, 5, 10})
	rb := NewRecorder([]float64{2, 5, 20})
	a1 := []float64{0.5, 3, 9}
	a2 := []float64{50, 60}
	ra.RecordBatch(a1)
	undoID := ra.RecordBatch(a2)
	rb.RecordBatch([]float64{2, 4, 30})

	ra.Undo(undoID)
	merged := Merge(ra.Histogram(), rb.Histogram())
	assertValid(t, merged)

	wantBoundaries := []float64{2, 5}
	if !reflect.DeepEqual(merged.Boundaries, wantBoundaries) {
		t.Fatalf("merged boundaries = %v, want %v", merged.Boundaries, wantBoundaries)
	}
	all := append(append([]float64{}, a1...), 2, 4, 30)
	for i, b := range merged.Boundaries {
		if got, want := merged.Cumulative[i], countLE(all, b); got != want {
			t.Errorf("merged cumulative at %v = %d, want exact %d", b, got, want)
		}
	}
	if merged.Total != uint64(len(all)) {
		t.Errorf("merged total = %d, want %d", merged.Total, len(all))
	}
}
