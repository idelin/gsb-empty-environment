package main

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestValidateAcceptsCumulative(t *testing.T) {
	h := Histogram{Bounds: []float64{1, 2, 3}, Counts: []int64{0, 5, 5}}
	if err := h.Validate(); err != nil {
		t.Fatalf("expected valid histogram, got %v", err)
	}
}

func TestValidateRejectsCountDecrease(t *testing.T) {
	h := Histogram{Bounds: []float64{1, 2, 3}, Counts: []int64{5, 4, 9}}
	err := h.Validate()
	if !errors.Is(err, ErrCountDecrease) {
		t.Fatalf("expected ErrCountDecrease, got %v", err)
	}
}

func TestValidateRejectsBadStructure(t *testing.T) {
	cases := []struct {
		name string
		h    Histogram
		want error
	}{
		{"length mismatch", Histogram{Bounds: []float64{1}, Counts: []int64{1, 2}}, ErrLengthMismatch},
		{"empty", Histogram{}, ErrEmpty},
		{"bounds not increasing", Histogram{Bounds: []float64{2, 2}, Counts: []int64{1, 2}}, ErrBoundsOrder},
		{"bounds descending", Histogram{Bounds: []float64{3, 1}, Counts: []int64{1, 2}}, ErrBoundsOrder},
		{"NaN bound", Histogram{Bounds: []float64{math.NaN()}, Counts: []int64{1}}, ErrBoundsOrder},
		{"negative count", Histogram{Bounds: []float64{1}, Counts: []int64{-1}}, ErrCountNegative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.h.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestMergeIdenticalBoundsAddsDirectly(t *testing.T) {
	a := Histogram{Bounds: []float64{10, 20, 30}, Counts: []int64{1, 4, 9}}
	b := Histogram{Bounds: []float64{10, 20, 30}, Counts: []int64{2, 3, 8}}
	got, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := Histogram{Bounds: []float64{10, 20, 30}, Counts: []int64{3, 7, 17}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMergeSubsetBoundsConvertsFineToCoarse(t *testing.T) {
	fine := Histogram{Bounds: []float64{5, 10, 20, 50, 100}, Counts: []int64{3, 8, 15, 30, 40}}
	coarse := Histogram{Bounds: []float64{10, 50, 100}, Counts: []int64{5, 20, 35}}
	want := Histogram{Bounds: []float64{10, 50, 100}, Counts: []int64{13, 50, 75}}

	got, err := Merge(fine, coarse)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fine+coarse: got %+v, want %+v", got, want)
	}

	// Order must not matter.
	got, err = Merge(coarse, fine)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coarse+fine: got %+v, want %+v", got, want)
	}
}

func TestMergeRejectsIncompatibleBounds(t *testing.T) {
	a := Histogram{Bounds: []float64{10, 20}, Counts: []int64{1, 2}}
	b := Histogram{Bounds: []float64{15, 25}, Counts: []int64{1, 2}}
	_, err := Merge(a, b)
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
}

func TestMergeRejectsPartialOverlap(t *testing.T) {
	// {10, 20, 30} vs {10, 30}: subset, OK. {10, 20} vs {20, 30}: not.
	a := Histogram{Bounds: []float64{10, 20}, Counts: []int64{1, 2}}
	b := Histogram{Bounds: []float64{20, 30}, Counts: []int64{1, 2}}
	if _, err := Merge(a, b); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
}

func TestMergeDetectsOverflow(t *testing.T) {
	a := Histogram{Bounds: []float64{1, 2}, Counts: []int64{math.MaxInt64 - 1, math.MaxInt64}}
	b := Histogram{Bounds: []float64{1, 2}, Counts: []int64{5, 5}}
	_, err := Merge(a, b)
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected ErrOverflow, got %v", err)
	}
}

func TestMergeValidatesInputs(t *testing.T) {
	bad := Histogram{Bounds: []float64{1, 2}, Counts: []int64{3, 1}}
	good := Histogram{Bounds: []float64{1, 2}, Counts: []int64{1, 2}}
	if _, err := Merge(bad, good); !errors.Is(err, ErrCountDecrease) {
		t.Fatalf("expected ErrCountDecrease, got %v", err)
	}
	if _, err := Merge(good, bad); !errors.Is(err, ErrCountDecrease) {
		t.Fatalf("expected ErrCountDecrease, got %v", err)
	}
}

func TestConvertToRefusesCoarseBucketSplit(t *testing.T) {
	h := Histogram{Bounds: []float64{10, 20}, Counts: []int64{5, 9}}
	// 15 lies inside the coarse bucket (10, 20]; refining it is refused.
	if _, err := h.ConvertTo([]float64{10, 15, 20}); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
}

func TestLimitsBracketsMergedCounts(t *testing.T) {
	a := Histogram{Bounds: []float64{64, 128, 256, 512}, Counts: []int64{2, 6, 9, 12}}
	b := Histogram{Bounds: []float64{100, 200, 300}, Counts: []int64{1, 4, 7}}
	target := unionBounds(a.Bounds, b.Bounds)
	r, err := Limits(a, b, target)
	if err != nil {
		t.Fatal(err)
	}
	wantMin := []int64{2, 3, 7, 10, 13, 16, 19}
	wantMax := []int64{3, 7, 10, 13, 16, 19, 19}
	if !reflect.DeepEqual(r.Min, wantMin) || !reflect.DeepEqual(r.Max, wantMax) {
		t.Fatalf("got min=%v max=%v, want min=%v max=%v", r.Min, r.Max, wantMin, wantMax)
	}
	// The total at the largest bound must be exact: both totals are known.
	if r.Min[len(r.Min)-1] != 19 || r.Max[len(r.Max)-1] != 19 {
		t.Fatalf("total interval should be exact [19,19], got [%d,%d]",
			r.Min[len(r.Min)-1], r.Max[len(r.Max)-1])
	}
}

func TestLimitsExactWhenBoundsAlign(t *testing.T) {
	a := Histogram{Bounds: []float64{10, 20}, Counts: []int64{3, 7}}
	b := Histogram{Bounds: []float64{10, 20}, Counts: []int64{1, 2}}
	r, err := Limits(a, b, []float64{10, 20})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Min, r.Max) {
		t.Fatalf("aligned bounds should give exact limits, got min=%v max=%v", r.Min, r.Max)
	}
	if !reflect.DeepEqual(r.Min, []int64{4, 9}) {
		t.Fatalf("got %v, want [4 9]", r.Min)
	}
}

func TestLimitsDetectsOverflow(t *testing.T) {
	a := Histogram{Bounds: []float64{1}, Counts: []int64{math.MaxInt64}}
	b := Histogram{Bounds: []float64{1}, Counts: []int64{1}}
	if _, err := Limits(a, b, []float64{1}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected ErrOverflow, got %v", err)
	}
}

func TestLimitsRejectsUnsortedTarget(t *testing.T) {
	a := Histogram{Bounds: []float64{1}, Counts: []int64{1}}
	if _, err := Limits(a, a, []float64{2, 1}); !errors.Is(err, ErrBoundsOrder) {
		t.Fatalf("expected ErrBoundsOrder, got %v", err)
	}
}

func TestUnionBounds(t *testing.T) {
	got := unionBounds([]float64{1, 3, 5}, []float64{2, 3, 4})
	want := []float64{1, 2, 3, 4, 5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
