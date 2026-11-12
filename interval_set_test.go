package netsec

import "testing"

func TestIntervalSetAddMerge(t *testing.T) {
	var s intervalSet
	s.add(3, 5)
	s.add(1, 2) // adjacent, merges to [1,5]
	s.add(10, 10)
	s.add(4, 9) // overlaps and bridges, merges to [1,10]

	want := []interval{{first: 1, last: 10}}
	if !equalIntervals(s.ranges, want) {
		t.Fatalf("ranges = %v, want %v", s.ranges, want)
	}
	for pn := uint64(1); pn <= 10; pn++ {
		if !s.contains(pn) {
			t.Fatalf("missing point %d", pn)
		}
	}
	if s.contains(0) || s.contains(11) {
		t.Fatal("contains point outside merged interval")
	}
	if !s.covers(1, 10) || s.covers(1, 11) {
		t.Fatal("covers reported incorrectly")
	}
	if got := s.count(); got != 10 {
		t.Fatalf("count = %d, want 10", got)
	}
	if max, ok := s.max(); !ok || max != 10 {
		t.Fatalf("max = %d,%v", max, ok)
	}
}

func TestIntervalSetRemoveSplit(t *testing.T) {
	var s intervalSet
	s.add(1, 10)
	s.remove(4, 7)
	want := []interval{{first: 1, last: 3}, {first: 8, last: 10}}
	if !equalIntervals(s.ranges, want) {
		t.Fatalf("ranges = %v, want %v", s.ranges, want)
	}
	if s.contains(4) || s.covers(1, 10) {
		t.Fatal("removed points still covered")
	}

	s.remove(1, 10)
	if !s.empty() {
		t.Fatalf("set not empty after full remove: %v", s.ranges)
	}
}

func TestIntervalSetUInt64Edges(t *testing.T) {
	var s intervalSet
	max := ^uint64(0)
	s.add(max, max)
	s.add(max-2, max-1) // adjacent to max, must not overflow on last+1
	want := []interval{{first: max - 2, last: max}}
	if !equalIntervals(s.ranges, want) {
		t.Fatalf("ranges = %v, want %v", s.ranges, want)
	}
	s.remove(max, max)
	if !s.contains(max-1) || s.contains(max) {
		t.Fatal("edge remove wrong")
	}
}

func equalIntervals(got, want []interval) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
