package calib

import (
	"math/big"
	"testing"
)

func feasibleCalib() Calib {
	return Calib{
		Feasible: true,
		OffsetLo: big.NewInt(3),
		OffsetHi: big.NewInt(7),
		DriftLo:  rat("9/5"),
		DriftHi:  rat("5/2"),
	}
}

func TestEventWindow(t *testing.T) {
	w, ok := EventWindow(rat("30"), feasibleCalib())
	if !ok {
		t.Fatal("expected alignable")
	}
	// corners: (30-7)/(5/2)=46/5, (30-3)/(9/5)=15
	if w.Lo.RatString() != "46/5" || w.Hi.RatString() != "15" {
		t.Errorf("window = [%s, %s], want [46/5, 15]", w.Lo, w.Hi)
	}
}

func TestEventWindowRejectsNonPositiveDrift(t *testing.T) {
	c := feasibleCalib()
	c.DriftLo = rat("0")
	if _, ok := EventWindow(rat("10"), c); ok {
		t.Fatal("non-positive drift must not align")
	}
}

func sampleEvents() ([]Event, map[string]Calib) {
	calA := feasibleCalib()
	calC := Calib{
		Feasible: true,
		OffsetLo: big.NewInt(-1),
		OffsetHi: big.NewInt(1),
		DriftLo:  rat("4/5"),
		DriftHi:  rat("6/5"),
	}
	events := []Event{
		{ID: "e1", Channel: "A", Reading: rat("30")},
		{ID: "e2", Channel: "C", Reading: rat("5")},
		{ID: "e3", Channel: "A", Reading: rat("12")},
		{ID: "e4", Channel: "C", Reading: rat("15")},
	}
	return events, map[string]Calib{"A": calA, "C": calC}
}

func hasPair(pairs []Pair, a, b string) bool {
	for _, p := range pairs {
		if p.First == a && p.Second == b {
			return true
		}
	}
	return false
}

func TestPartialOrder(t *testing.T) {
	events, calibs := sampleEvents()
	prec, inc := PartialOrder(events, calibs)

	// Definite precedences.
	for _, want := range [][2]string{
		{"e3", "e1"}, // same channel, 12 < 30
		{"e3", "e4"}, // e3 window [2,5] < e4 window [35/3,20]
		{"e2", "e1"}, // e2 window [10/3,15/2] < e1 window [46/5,15]
		{"e2", "e4"}, // 15/2 < 35/3
	} {
		if !hasPair(prec, want[0], want[1]) {
			t.Errorf("expected precedence %s < %s, prec=%v", want[0], want[1], prec)
		}
	}
	// Undetermined pairs must stay incomparable, never midpoint-sorted:
	// e1 vs e4 windows [46/5,15] and [35/3,20] overlap, and e2 vs e3
	// windows [10/3,15/2] and [2,5] overlap.
	for _, want := range [][2]string{
		{"e1", "e4"},
		{"e2", "e3"},
	} {
		if !hasPair(inc, want[0], want[1]) && !hasPair(inc, want[1], want[0]) {
			t.Errorf("expected %s and %s incomparable, inc=%v", want[0], want[1], inc)
		}
		if hasPair(prec, want[0], want[1]) || hasPair(prec, want[1], want[0]) {
			t.Errorf("%s and %s must not be ordered", want[0], want[1])
		}
	}
	if len(prec)+len(inc) != 6 {
		t.Errorf("4 events => 6 pairs, got %d", len(prec)+len(inc))
	}
}

func TestSameChannelEqualReadingsIncomparable(t *testing.T) {
	events := []Event{
		{ID: "x", Channel: "A", Reading: rat("10")},
		{ID: "y", Channel: "A", Reading: rat("10")},
	}
	prec, inc := PartialOrder(events, map[string]Calib{"A": feasibleCalib()})
	if len(prec) != 0 || len(inc) != 1 {
		t.Errorf("equal readings must be incomparable, prec=%v inc=%v", prec, inc)
	}
}
