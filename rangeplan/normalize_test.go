package rangeplan

import (
	"errors"
	"math"
	"testing"
)

func TestNormalizeClosed(t *testing.T) {
	cases := []struct {
		spec Spec
		size int64
		want Range
	}{
		{Spec{Start: 0, End: 99}, 100, Range{0, 99}},
		{Spec{Start: 0, End: 999}, 100, Range{0, 99}}, // clipped
		{Spec{Start: 50, End: 149}, 100, Range{50, 99}},
		{Spec{Start: 99, End: 99}, 100, Range{99, 99}},
	}
	for _, tc := range cases {
		got, err := Normalize(tc.spec, tc.size)
		if err != nil {
			t.Fatalf("Normalize(%+v,%d) error: %v", tc.spec, tc.size, err)
		}
		if got != tc.want {
			t.Errorf("Normalize(%+v,%d) = %+v, want %+v", tc.spec, tc.size, got, tc.want)
		}
	}
}

func TestNormalizeOpenEnded(t *testing.T) {
	got, err := Normalize(Spec{Start: 950, OpenEnd: true}, 1000)
	if err != nil || got != (Range{950, 999}) {
		t.Fatalf("open-ended normalize: %+v err=%v", got, err)
	}
	if _, err = Normalize(Spec{Start: 1000, OpenEnd: true}, 1000); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("start == size: err=%v, want ErrUnsatisfiable", err)
	}
}

func TestNormalizeSuffix(t *testing.T) {
	cases := []struct {
		n    int64
		size int64
		want Range
	}{
		{500, 1000, Range{500, 999}},
		{5000, 1000, Range{0, 999}}, // suffix larger than object
		{1, 1, Range{0, 0}},
	}
	for _, tc := range cases {
		got, err := Normalize(Spec{End: tc.n, Suffix: true}, tc.size)
		if err != nil {
			t.Fatalf("suffix %d size %d error: %v", tc.n, tc.size, err)
		}
		if got != tc.want {
			t.Errorf("suffix %d size %d = %+v, want %+v", tc.n, tc.size, got, tc.want)
		}
	}

	for _, n := range []int64{0, 1, 10} {
		if _, err := Normalize(Spec{End: n, Suffix: true}, 0); !errors.Is(err, ErrUnsatisfiable) {
			t.Fatalf("suffix on empty object: err=%v", err)
		}
	}
}

func TestNormalizeUnsatisfiable(t *testing.T) {
	bad := []Spec{
		{Start: 10, End: 20},
		{Start: 10, OpenEnd: true},
	}
	for _, spec := range bad {
		if _, err := Normalize(spec, 10); !errors.Is(err, ErrUnsatisfiable) {
			t.Errorf("Normalize(%+v,10) err=%v, want ErrUnsatisfiable", spec, err)
		}
	}

	// One unsatisfiable range among good ones is dropped, not fatal.
	ranges, err := NormalizeAll([]Spec{
		{Start: 0, End: 4},
		{Start: 99, OpenEnd: true},
		{End: 2, Suffix: true},
	}, 10)
	if err != nil {
		t.Fatalf("NormalizeAll error: %v", err)
	}
	want := []Range{{0, 4}, {8, 9}}
	if len(ranges) != len(want) {
		t.Fatalf("NormalizeAll = %+v, want %+v", ranges, want)
	}
	for i := range want {
		if ranges[i] != want[i] {
			t.Errorf("NormalizeAll[%d] = %+v, want %+v", i, ranges[i], want[i])
		}
	}

	if _, err := NormalizeAll([]Spec{{Start: 5, End: 9}}, 3); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("all unsatisfiable: err=%v", err)
	}
}

func TestNormalizeOverflowGuards(t *testing.T) {
	if _, err := Normalize(Spec{Start: 0, End: 0}, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("negative size should fail: %v", err)
	}
	// Suffix at the int64 boundary must not underflow when clipped.
	got, err := Normalize(Spec{End: math.MaxInt64, Suffix: true}, math.MaxInt64)
	if err != nil {
		t.Fatalf("max-int suffix errored: %v", err)
	}
	if got != (Range{0, math.MaxInt64 - 1}) {
		t.Fatalf("max-int suffix = %+v", got)
	}
}
