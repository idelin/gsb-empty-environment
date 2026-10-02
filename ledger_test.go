package bitemporal

import (
	"errors"
	"testing"
)

// acceptanceLedger builds the scenario from the acceptance criteria:
// price 10 valid on business [0,20) known at 2;
// price 15 valid on business [5,10) known at 8.
func acceptanceLedger() *Ledger {
	l := NewLedger()
	l.Add(Rule{ID: "p10", ValidFrom: 0, ValidTo: 20, PriceCents: 10, KnownAt: 2})
	l.Add(Rule{ID: "p15", ValidFrom: 5, ValidTo: 10, PriceCents: 15, KnownAt: 8})
	return l
}

func TestAcceptance(t *testing.T) {
	l := acceptanceLedger()
	cases := []struct {
		name      string
		validTime int64
		asOf      int64
		want      int64
	}{
		{"business 7 known at 7 -> 10", 7, 7, 10},
		{"business 7 known at 8 -> 15", 7, 8, 15},
		{"business 10 known at 8 -> 10", 10, 8, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := l.PriceAt(tc.validTime, tc.asOf)
			if err != nil {
				t.Fatalf("PriceAt(%d, %d) error: %v", tc.validTime, tc.asOf, err)
			}
			if got != tc.want {
				t.Fatalf("PriceAt(%d, %d) = %d, want %d", tc.validTime, tc.asOf, got, tc.want)
			}
		})
	}
}

func TestLeftClosedRightOpen(t *testing.T) {
	l := acceptanceLedger()
	// Boundary 5 is inside [5,10); boundary 10 is outside it.
	if got, err := l.PriceAt(5, 8); err != nil || got != 15 {
		t.Fatalf("PriceAt(5, 8) = %d, %v; want 15, nil", got, err)
	}
	if got, err := l.PriceAt(10, 8); err != nil || got != 10 {
		t.Fatalf("PriceAt(10, 8) = %d, %v; want 10, nil", got, err)
	}
	// Boundary 0 is inside [0,20); boundary 20 is outside it.
	if got, err := l.PriceAt(0, 8); err != nil || got != 10 {
		t.Fatalf("PriceAt(0, 8) = %d, %v; want 10, nil", got, err)
	}
	if _, err := l.PriceAt(20, 8); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PriceAt(20, 8) err = %v, want ErrNotFound", err)
	}
}

func TestUnknownRuleNotVisible(t *testing.T) {
	l := acceptanceLedger()
	// Before transaction time 2 nothing is known.
	if _, err := l.PriceAt(7, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("PriceAt(7, 1) err = %v, want ErrNotFound", err)
	}
}

func TestRetractionOnlyAffectsLaterKnowledge(t *testing.T) {
	l := acceptanceLedger()
	l.Retract("p15", 12)

	// As-of 11 (before the retraction) the 15-price is still visible.
	if got, err := l.PriceAt(7, 11); err != nil || got != 15 {
		t.Fatalf("PriceAt(7, 11) = %d, %v; want 15, nil", got, err)
	}
	// As-of 12 (at the retraction) it is gone; the 10-price shows through.
	if got, err := l.PriceAt(7, 12); err != nil || got != 10 {
		t.Fatalf("PriceAt(7, 12) = %d, %v; want 10, nil", got, err)
	}
	// As-of 20 (after the retraction) it stays gone.
	if got, err := l.PriceAt(7, 20); err != nil || got != 10 {
		t.Fatalf("PriceAt(7, 20) = %d, %v; want 10, nil", got, err)
	}
}

func TestLaterKnowledgeWinsOnOverlap(t *testing.T) {
	l := NewLedger()
	l.Add(Rule{ID: "a", ValidFrom: 0, ValidTo: 100, PriceCents: 100, KnownAt: 1})
	l.Add(Rule{ID: "b", ValidFrom: 0, ValidTo: 100, PriceCents: 200, KnownAt: 3})
	l.Add(Rule{ID: "c", ValidFrom: 0, ValidTo: 100, PriceCents: 300, KnownAt: 2})

	// Fully overlapping rules: greatest KnownAt wins.
	if got, err := l.PriceAt(50, 10); err != nil || got != 200 {
		t.Fatalf("PriceAt(50, 10) = %d, %v; want 200, nil", got, err)
	}
	// As-of 2 hides rule b, so rule c (KnownAt 2) wins over a (KnownAt 1).
	if got, err := l.PriceAt(50, 2); err != nil || got != 300 {
		t.Fatalf("PriceAt(50, 2) = %d, %v; want 300, nil", got, err)
	}
}
