package order

import (
	"math/big"
	"testing"

	"clockcal/internal/calib"
)

func ev(id string, lo, hi string) Event {
	mk := func(s string) *big.Rat {
		r, _ := calib.ParseRat(s)
		return r
	}
	return Event{ID: id, Interval: calib.Interval{Lo: mk(lo), Hi: mk(hi)}}
}

func TestPartialOrder(t *testing.T) {
	events := []Event{
		ev("e1", "10", "12"),
		ev("e2", "60", "63"),
		ev("e3", "61", "62"), // overlaps e2
		ev("e4", "110", "114"),
	}
	rel := Build(events)

	// e1 certainly precedes everyone; e4 certainly follows everyone.
	for _, j := range []int{1, 2, 3} {
		if !rel.Before[0][j] {
			t.Fatalf("expected e1 before e%d", j+1)
		}
	}
	for _, j := range []int{1, 2} {
		if !rel.Before[j][3] {
			t.Fatalf("expected e%d before e4", j+1)
		}
	}
	if rel.Before[1][2] || rel.Before[2][1] {
		t.Fatalf("e2 and e3 must be incomparable")
	}

	edges := rel.TransitiveReduction()
	if len(edges) != 4 {
		t.Fatalf("expected 4 reduced edges, got %v", edges)
	}
	for _, e := range edges {
		if e[0] == 0 && e[1] == 3 {
			t.Fatalf("e1->e4 must be absorbed by transitivity")
		}
	}

	inc := rel.Incomparable()
	if len(inc) != 1 || inc[0] != [2]int{1, 2} {
		t.Fatalf("expected only (e2,e3) incomparable, got %v", inc)
	}
}

func TestTouchingIntervalsAreIncomparable(t *testing.T) {
	// [1,2] and [2,3] can tie at 2, so no strict order may be inferred.
	events := []Event{ev("a", "1", "2"), ev("b", "2", "3")}
	rel := Build(events)
	if rel.Before[0][1] || rel.Before[1][0] {
		t.Fatalf("touching intervals must stay incomparable")
	}
}

func TestInfiniteBoundsNeverOrder(t *testing.T) {
	events := []Event{
		{ID: "u", Interval: calib.Interval{}}, // (-inf, +inf)
		ev("b", "0", "1"),
	}
	rel := Build(events)
	if rel.Before[0][1] || rel.Before[1][0] {
		t.Fatalf("unbounded event must stay incomparable")
	}
}
