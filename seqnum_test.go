package seqwin

import "testing"

func mustSpace(t *testing.T, bits uint) *Space {
	t.Helper()
	sp, err := NewSpace(bits)
	if err != nil {
		t.Fatalf("NewSpace(%d): %v", bits, err)
	}
	return sp
}

func TestEightBitWrapIsConsecutive(t *testing.T) {
	sp := mustSpace(t, 8)
	seq := []uint64{254, 255, 0, 1}
	for i := 0; i+1 < len(seq); i++ {
		if got := sp.Compare(seq[i+1], seq[i]); got != Newer {
			t.Errorf("Compare(%d,%d) = %v, want newer", seq[i+1], seq[i], got)
		}
		if d := sp.Dist(seq[i], seq[i+1]); d != 1 {
			t.Errorf("Dist(%d,%d) = %d, want 1", seq[i], seq[i+1], d)
		}
	}
	// And the reverse direction reads as strictly older.
	if got := sp.Compare(254, 1); got != Older {
		t.Errorf("Compare(254,1) = %v, want older", got)
	}
}

func TestHalfSpaceDistanceIsAmbiguous(t *testing.T) {
	sp := mustSpace(t, 8)
	// 128 is exactly half of 256: neither newer nor older may be assumed.
	if got := sp.Compare(128, 0); got != Ambiguous {
		t.Errorf("Compare(128,0) = %v, want ambiguous", got)
	}
	if got := sp.Compare(0, 128); got != Ambiguous {
		t.Errorf("Compare(0,128) = %v, want ambiguous", got)
	}
	// One step inside the half boundary is still decidable.
	if got := sp.Compare(127, 0); got != Newer {
		t.Errorf("Compare(127,0) = %v, want newer", got)
	}
	if got := sp.Compare(129, 0); got != Older {
		t.Errorf("Compare(129,0) = %v, want older", got)
	}
}

func TestCompareAcrossWrap(t *testing.T) {
	sp := mustSpace(t, 8)
	cases := []struct {
		a, b uint64
		want Relation
	}{
		{0, 255, Newer},   // 0 follows 255
		{255, 0, Older},   // 255 precedes 0
		{200, 100, Newer}, // plain forward
		{100, 200, Older}, // plain backward
		{7, 7, Equal},
	}
	for _, c := range cases {
		if got := sp.Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%d,%d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestNewSpaceRejectsBadWidths(t *testing.T) {
	for _, bits := range []uint{0, 1, 33, 64} {
		if _, err := NewSpace(bits); err == nil {
			t.Errorf("NewSpace(%d) succeeded, want error", bits)
		}
	}
}
