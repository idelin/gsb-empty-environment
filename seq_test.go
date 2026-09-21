package seqwin

import (
	"errors"
	"testing"
)

var space8 = NewSpace(8)

func mustLess(t *testing.T, a, b uint32) {
	t.Helper()
	less, err := space8.Less(a, b)
	if err != nil || !less {
		t.Fatalf("Less(%d, %d) = %v, %v; want true, nil", a, b, less, err)
	}
}

// 254, 255, 0, 1 must be consecutive across the wrap point.
func TestConsecutiveAcrossWrap(t *testing.T) {
	mustLess(t, 254, 255)
	mustLess(t, 255, 0)
	mustLess(t, 0, 1)
	for _, tc := range []struct {
		a, b uint32
		want int32
	}{
		{255, 254, 1}, {0, 254, 2}, {1, 254, 3},
		{254, 255, -1}, {254, 0, -2}, {7, 7, 0},
	} {
		d, ok := space8.Diff(tc.a, tc.b)
		if !ok || d != tc.want {
			t.Errorf("Diff(%d, %d) = %d, %v; want %d, true", tc.a, tc.b, d, ok, tc.want)
		}
	}
}

// A distance of exactly 128 (half the 8-bit space) is ambiguous and must
// not be resolved as either old or new.
func TestHalfSpaceAmbiguous(t *testing.T) {
	for _, tc := range [][2]uint32{{0, 128}, {128, 0}, {1, 129}, {200, 72}} {
		if d, ok := space8.Diff(tc[0], tc[1]); ok {
			t.Errorf("Diff(%d, %d) = %d, true; want ok=false", tc[0], tc[1], d)
		}
		if _, err := space8.Less(tc[0], tc[1]); !errors.Is(err, ErrAmbiguous) {
			t.Errorf("Less(%d, %d) err = %v; want ErrAmbiguous", tc[0], tc[1], err)
		}
	}
}

func TestDiffSigns(t *testing.T) {
	if d, _ := space8.Diff(200, 100); d != 100 {
		t.Errorf("Diff(200,100) = %d; want 100", d)
	}
	if d, _ := space8.Diff(100, 200); d != -100 {
		t.Errorf("Diff(100,200) = %d; want -100", d)
	}
	if d, _ := space8.Diff(3, 253); d != 6 {
		t.Errorf("Diff(3,253) = %d; want 6", d)
	}
}
