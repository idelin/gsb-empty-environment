package interval

import (
	"math/big"
	"testing"
)

func TestUnionMerging(t *testing.T) {
	// Overlapping intervals merge into one segment.
	s := Closed(rat(1, 1), rat(3, 1)).Union(Closed(rat(2, 1), rat(4, 1)))
	if len(s.Segments) != 1 || !s.Contains(rat(1, 1)) || !s.Contains(rat(4, 1)) {
		t.Errorf("got %v, want single [1, 4]", s)
	}
	// Touching at an included point merges.
	s = Closed(rat(1, 1), rat(2, 1)).Union(Make(Fin(rat(2, 1)), Fin(rat(3, 1)), true, false))
	if len(s.Segments) != 1 {
		t.Errorf("[1,2] ∪ (2,3] should merge, got %v", s)
	}
	// Touching at an excluded point stays split.
	s = Make(Fin(rat(1, 1)), Fin(rat(2, 1)), false, true).
		Union(Make(Fin(rat(2, 1)), Fin(rat(3, 1)), true, false))
	if len(s.Segments) != 2 || s.Contains(rat(2, 1)) {
		t.Errorf("[1,2) ∪ (2,3] should stay split around 2, got %v", s)
	}
	// Disjoint with a gap stays split.
	s = Closed(rat(1, 1), rat(2, 1)).Union(Closed(rat(5, 1), rat(6, 1)))
	if len(s.Segments) != 2 || s.Contains(rat(3, 1)) {
		t.Errorf("disjoint union wrong: %v", s)
	}
}

func TestSetIntersect(t *testing.T) {
	a := Closed(rat(0, 1), rat(10, 1))
	b := Closed(rat(-5, 1), rat(1, 1)).Union(Closed(rat(5, 1), rat(15, 1)))
	got := NewSet(0)
	got.Add(a)
	got = got.Intersect(b)
	if len(got.Segments) != 2 {
		t.Fatalf("want 2 segments, got %v", got)
	}
	for _, v := range []*big.Rat{rat(0, 1), rat(1, 1), rat(5, 1), rat(10, 1)} {
		if !got.Contains(v) {
			t.Errorf("%v must contain %v", got, v)
		}
	}
	for _, v := range []*big.Rat{rat(2, 1), rat(4, 1), rat(11, 1), rat(-1, 1)} {
		if got.Contains(v) {
			t.Errorf("%v must not contain %v", got, v)
		}
	}
}

func TestMaxSegmentsConservativeMerge(t *testing.T) {
	s := NewSet(2)
	points := []int64{0, 2, 4, 10}
	for _, p := range points {
		s.Add(Point(rat(p, 1)))
	}
	if len(s.Segments) > 2 {
		t.Fatalf("segment cap violated: %v", s)
	}
	// Conservative: every original point is still contained.
	for _, p := range points {
		if !s.Contains(rat(p, 1)) {
			t.Errorf("merged set %v lost point %d", s, p)
		}
	}
	// Merging is by hull: the closest pair {0},{2} merges first, so the
	// gap point 1 becomes included (superset, never subset).
	if !s.Contains(rat(1, 1)) {
		t.Errorf("hull merge should conservatively include gap point 1: %v", s)
	}
	// Nothing outside the outer hull may appear.
	if s.Contains(rat(-1, 1)) || s.Contains(rat(11, 1)) {
		t.Errorf("merge must not exceed outer hull: %v", s)
	}
}

func TestSetEncloseRespectsCap(t *testing.T) {
	s := NewSet(2)
	s.Add(Closed(rat(1, 3), rat(2, 3)))
	s.Add(Closed(rat(10, 3), rat(11, 3)))
	s.Add(Closed(rat(20, 3), rat(21, 3)))
	enc := s.Enclose(1)
	if len(enc.Segments) > 2 {
		t.Fatalf("cap violated after Enclose: %v", enc)
	}
	// Original rational points remain contained after outward rounding.
	for _, v := range []*big.Rat{rat(1, 3), rat(2, 3), rat(10, 3), rat(11, 3), rat(20, 3), rat(21, 3)} {
		if !enc.Contains(v) {
			t.Errorf("enclosure %v lost %v", enc, v)
		}
	}
}
