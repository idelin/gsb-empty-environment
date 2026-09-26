package histmerge

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestValidateAcceptsMonotonicCounts(t *testing.T) {
	h := &Histogram{Bounds: []float64{0.5, 1, 5}, Counts: []int64{2, 2, 7}}
	if err := h.Validate(); err != nil {
		t.Fatalf("expected valid histogram, got %v", err)
	}
}

func TestValidateRejectsDecreasingCounts(t *testing.T) {
	h := &Histogram{Bounds: []float64{0.5, 1, 5}, Counts: []int64{3, 2, 7}}
	err := h.Validate()
	if err == nil || !strings.Contains(err.Error(), "decreased") {
		t.Fatalf("expected decreasing-count input error, got %v", err)
	}
}

func TestValidateRejectsBadBounds(t *testing.T) {
	cases := []Histogram{
		{Bounds: []float64{1, 1}, Counts: []int64{0, 0}},     // not increasing
		{Bounds: []float64{math.NaN()}, Counts: []int64{0}},  // NaN bound
		{Bounds: []float64{math.Inf(1)}, Counts: []int64{0}}, // infinite bound
		{Bounds: []float64{1, 2}, Counts: []int64{0}},        // length mismatch
		{Bounds: []float64{1}, Counts: []int64{-1}},          // negative count
	}
	for i, h := range cases {
		if err := h.Validate(); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestMergeIdenticalBoundsAddsDirectly(t *testing.T) {
	a := &Histogram{Bounds: []float64{0.5, 1, 5}, Counts: []int64{1, 3, 4}}
	b := &Histogram{Bounds: []float64{0.5, 1, 5}, Counts: []int64{2, 5, 9}}
	got, err := Merge(a, b)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if !reflect.DeepEqual(got.Counts, []int64{3, 8, 13}) {
		t.Fatalf("counts = %v, want [3 8 13]", got.Counts)
	}
	// Inputs must not be mutated.
	if !reflect.DeepEqual(a.Counts, []int64{1, 3, 4}) {
		t.Fatalf("input mutated: %v", a.Counts)
	}
}

func TestMergeProjectsFinerOntoCoarser(t *testing.T) {
	coarse := &Histogram{Bounds: []float64{0.5, 1, 5}, Counts: []int64{1, 2, 4}}
	fine := &Histogram{
		Bounds: []float64{0.1, 0.5, 0.75, 1, 2, 5},
		Counts: []int64{1, 3, 4, 6, 8, 10},
	}
	got, err := Merge(coarse, fine)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if !reflect.DeepEqual(got.Bounds, []float64{0.5, 1, 5}) {
		t.Fatalf("bounds = %v, want [0.5 1 5]", got.Bounds)
	}
	if !reflect.DeepEqual(got.Counts, []int64{4, 8, 14}) {
		t.Fatalf("counts = %v, want [4 8 14]", got.Counts)
	}
}

func TestMergeProjectionIsSymmetric(t *testing.T) {
	coarse := &Histogram{Bounds: []float64{1, 5}, Counts: []int64{2, 6}}
	fine := &Histogram{Bounds: []float64{0.5, 1, 2, 5}, Counts: []int64{1, 3, 5, 7}}
	got, err := Merge(fine, coarse)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if !reflect.DeepEqual(got.Bounds, []float64{1, 5}) ||
		!reflect.DeepEqual(got.Counts, []int64{5, 13}) {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeRejectsIncompatibleBoundsAndReportsThem(t *testing.T) {
	a := &Histogram{Bounds: []float64{0.5, 1, 5}, Counts: []int64{1, 2, 3}}
	b := &Histogram{Bounds: []float64{0.5, 2, 5}, Counts: []int64{1, 2, 3}}
	_, err := Merge(a, b)
	if err == nil {
		t.Fatal("expected rejection, got nil error")
	}
	msg := err.Error()
	for _, want := range []string{"[0.5 1 5]", "[0.5 2 5]", "never split"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestMergeRejectsInvalidInput(t *testing.T) {
	bad := &Histogram{Bounds: []float64{1, 2}, Counts: []int64{5, 4}} // decreasing
	ok := &Histogram{Bounds: []float64{1, 2}, Counts: []int64{1, 2}}
	if _, err := Merge(bad, ok); err == nil {
		t.Fatal("expected error for decreasing left input")
	}
	if _, err := Merge(ok, bad); err == nil {
		t.Fatal("expected error for decreasing right input")
	}
}

func TestMergeDetectsOverflow(t *testing.T) {
	a := &Histogram{Bounds: []float64{1}, Counts: []int64{math.MaxInt64 - 1}}
	b := &Histogram{Bounds: []float64{1}, Counts: []int64{2}}
	if _, err := Merge(a, b); err == nil ||
		!strings.Contains(err.Error(), "overflow") {
		t.Fatalf("expected overflow error, got %v", err)
	}
	// Just below the overflow threshold still merges.
	b.Counts[0] = 1
	got, err := Merge(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Counts[0] != math.MaxInt64 {
		t.Fatalf("count = %d, want %d", got.Counts[0], int64(math.MaxInt64))
	}
}

func TestProjectPreservesTotals(t *testing.T) {
	h := &Histogram{
		Bounds: []float64{0.1, 0.5, 1, 2, 5},
		Counts: []int64{2, 5, 9, 11, 20},
	}
	p, err := h.Project([]float64{0.5, 5})
	if err != nil {
		t.Fatalf("project failed: %v", err)
	}
	if !reflect.DeepEqual(p.Counts, []int64{5, 20}) {
		t.Fatalf("counts = %v, want [5 20]", p.Counts)
	}
	if _, err := h.Project([]float64{0.3}); err == nil {
		t.Fatal("expected projection onto non-subset bounds to fail")
	}
}

func TestObserveBuildsCumulativeCounts(t *testing.T) {
	h := New([]float64{0.5, 1, 5})
	for _, v := range []float64{0.2, 0.7, 0.9, 4.9} {
		if !h.Observe(v) {
			t.Fatalf("observe(%v) unexpectedly dropped", v)
		}
	}
	if h.Observe(42) {
		t.Fatal("observe beyond last bound should be dropped")
	}
	if !reflect.DeepEqual(h.Counts, []int64{1, 3, 4}) {
		t.Fatalf("counts = %v, want [1 3 4]", h.Counts)
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("observed histogram invalid: %v", err)
	}
}
