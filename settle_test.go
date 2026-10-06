package settle

import (
	"math/big"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(min int) time.Time { return epoch.Add(time.Duration(min) * time.Minute) }

func rat(p, q int64) *big.Rat { return big.NewRat(p, q) }

// twoRates: [0,60min) @ 1/unit, [60,120min) @ 3/unit.
func twoRates() []Rate {
	return []Rate{
		{Start: at(0), End: at(60), Price: rat(1, 1)},
		{Start: at(60), End: at(120), Price: rat(3, 1)},
	}
}

func totalIs(t *testing.T, s Settlement, p, q int64) {
	t.Helper()
	if s.Total.Cmp(rat(p, q)) != 0 {
		t.Fatalf("total = %s, want %d/%d", s.Total, p, q)
	}
}

func TestEventAtBoundaryBelongsToLaterInterval(t *testing.T) {
	s, err := Settle(twoRates(), []Event{
		{Time: at(60), Qty: rat(2, 1)}, // exactly at the seam
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 6, 1) // 2 units @ 3, not @ 1
	if len(s.Segments) != 1 || !s.Segments[0].Rate.Start.Equal(at(60)) {
		t.Fatalf("segments = %+v, want only the later interval", s.Segments)
	}
}

func TestEventAtIntervalEdges(t *testing.T) {
	s, err := Settle(twoRates(), []Event{
		{Time: at(0), Qty: rat(1, 1)},   // first instant of interval 1
		{Time: at(119), Qty: rat(1, 1)}, // inside interval 2
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 4, 1) // 1*1 + 1*3
}

func TestEventOutsideCoverageFails(t *testing.T) {
	if _, err := Settle(twoRates(), []Event{{Time: at(120), Qty: rat(1, 1)}}, nil); err == nil {
		t.Fatal("want error for event at end of last half-open interval")
	}
	if _, err := Settle(twoRates(), []Event{{Time: at(-1), Qty: rat(1, 1)}}, nil); err == nil {
		t.Fatal("want error for event before coverage")
	}
}

func TestLinearProratedByDuration(t *testing.T) {
	// 12 units over [30, 90): 30 min in each interval -> 6 @ 1 + 6 @ 3.
	s, err := Settle(twoRates(), nil, []Linear{
		{Start: at(30), End: at(90), Qty: rat(12, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 24, 1)
	if len(s.Segments) != 2 {
		t.Fatalf("segments = %+v, want 2", s.Segments)
	}
	if s.Segments[0].Qty.Cmp(rat(6, 1)) != 0 || s.Segments[1].Qty.Cmp(rat(6, 1)) != 0 {
		t.Fatalf("proration = %s, %s; want 6 and 6", s.Segments[0].Qty, s.Segments[1].Qty)
	}
}

func TestLinearUnevenSplitStaysRational(t *testing.T) {
	// 1 unit over [0, 120): 1/2 @ 1 + 1/2 @ 3 = 2, computed exactly.
	s, err := Settle(twoRates(), nil, []Linear{
		{Start: at(0), End: at(120), Qty: rat(1, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 2, 1)
	// 1 unit over [0, 100): 3/5 @ 1 + 2/5 @ 3 = 9/5.
	s, err = Settle(twoRates(), nil, []Linear{
		{Start: at(0), End: at(100), Qty: rat(1, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 9, 5)
}

func TestZeroDurationLinearContributesNothing(t *testing.T) {
	s, err := Settle(twoRates(), nil, []Linear{
		{Start: at(45), End: at(45), Qty: rat(1000, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 0, 1)
	if len(s.Segments) != 0 {
		t.Fatalf("segments = %+v, want none", s.Segments)
	}
	// Zero duration outside coverage is still a no-op.
	if _, err := Settle(twoRates(), nil, []Linear{
		{Start: at(500), End: at(500), Qty: rat(1, 1)},
	}); err != nil {
		t.Fatal(err)
	}
	// Negative duration is rejected.
	if _, err := Settle(twoRates(), nil, []Linear{
		{Start: at(50), End: at(40), Qty: rat(1, 1)},
	}); err == nil {
		t.Fatal("want error for negative-duration linear")
	}
}

func TestLinearNotFullyCoveredFails(t *testing.T) {
	if _, err := Settle(twoRates(), nil, []Linear{
		{Start: at(60), End: at(180), Qty: rat(1, 1)},
	}); err == nil {
		t.Fatal("want error for partially covered linear")
	}
}

func TestRoundHalfEvenCents(t *testing.T) {
	cases := []struct {
		p, q int64
		want int64
	}{
		{1, 200, 0},   // 0.005 -> 0
		{3, 200, 2},   // 0.015 -> 2
		{5, 200, 2},   // 0.025 -> 2 (tie to even)
		{7, 200, 4},   // 0.035 -> 4
		{-5, 200, -2}, // -0.025 -> -2
		{-7, 200, -4}, // -0.035 -> -4
		{1, 3, 33},    // 0.333... -> 33
		{2, 3, 67},    // 0.666... -> 67
		{0, 1, 0},
	}
	for _, c := range cases {
		got := RoundHalfEvenCents(rat(c.p, c.q))
		if got.Cmp(big.NewInt(c.want)) != 0 {
			t.Errorf("RoundHalfEvenCents(%d/%d) = %s, want %d", c.p, c.q, got, c.want)
		}
	}
}

func TestCentsRoundsOnlyGrandTotal(t *testing.T) {
	// Three segments of 0.005 each: exact total 0.015 -> 2 cents.
	// Rounding each segment first would give 0.
	rates := []Rate{{Start: at(0), End: at(60), Price: rat(1, 200)}}
	s, err := Settle(rates, []Event{
		{Time: at(1), Qty: rat(1, 1)},
		{Time: at(2), Qty: rat(1, 1)},
		{Time: at(3), Qty: rat(1, 1)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Cents(); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("cents = %s, want 2", got)
	}
}

func ratesWithPrice(min int, price *big.Rat) []Rate {
	return []Rate{{Start: at(min), End: at(min + 60), Price: price}}
}

func usage() ([]Event, []Linear) {
	return []Event{{Time: at(10), Qty: rat(3, 1)}},
		[]Linear{{Start: at(0), End: at(30), Qty: rat(2, 1)}}
}

func TestRetroactiveChangeOutputsDelta(t *testing.T) {
	events, linears := usage()
	before, err := Settle(ratesWithPrice(0, rat(10, 1)), events, linears)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Settle(ratesWithPrice(0, rat(12, 1)), events, linears)
	if err != nil {
		t.Fatal(err)
	}
	// Exact totals 50 and 60 -> delta +1000 cents, not a re-bill of 6000.
	d := DeltaCents(before, after)
	if d.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("delta = %s cents, want 1000", d)
	}
	if d.Cmp(after.Cents()) == 0 {
		t.Fatal("delta must not equal the re-billed total")
	}
}

func TestRepeatedRevisionsTelescope(t *testing.T) {
	events, linears := usage()
	prices := []*big.Rat{
		rat(10, 1),   // original
		rat(12, 1),   // revision 1
		rat(11, 1),   // revision 2
		rat(13, 200), // revision 3: forces half-even rounding at 0.065/unit
	}
	var settlements []Settlement
	for _, p := range prices {
		s, err := Settle(ratesWithPrice(0, p), events, linears)
		if err != nil {
			t.Fatal(err)
		}
		settlements = append(settlements, s)
	}
	chain := new(big.Int)
	for i := 1; i < len(settlements); i++ {
		chain.Add(chain, DeltaCents(settlements[i-1], settlements[i]))
	}
	direct := DeltaCents(settlements[0], settlements[len(settlements)-1])
	if chain.Cmp(direct) != 0 {
		t.Fatalf("sum of revision deltas = %s, direct delta = %s; ledger does not telescope", chain, direct)
	}
}

func TestRetroactiveChangeOnOneOfSeveralIntervals(t *testing.T) {
	events := []Event{
		{Time: at(30), Qty: rat(4, 1)},
		{Time: at(90), Qty: rat(4, 1)},
	}
	before, err := Settle(twoRates(), events, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := twoRates()
	changed[1].Price = rat(5, 1)
	after, err := Settle(changed, events, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Only interval 2 changed: 4*(5-3) = 8 -> 800 cents.
	if d := DeltaCents(before, after); d.Cmp(big.NewInt(800)) != 0 {
		t.Fatalf("delta = %s cents, want 800", d)
	}
}

func TestExactRationalAccumulation(t *testing.T) {
	// 1/3 unit @ 1/7 per unit, three times: 1/7 exactly, no float drift.
	rates := []Rate{{Start: at(0), End: at(60), Price: rat(1, 7)}}
	s, err := Settle(rates, []Event{
		{Time: at(1), Qty: rat(1, 3)},
		{Time: at(2), Qty: rat(1, 3)},
		{Time: at(3), Qty: rat(1, 3)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	totalIs(t, s, 1, 7)
}

func TestInvalidRatesRejected(t *testing.T) {
	bad := []Rate{{Start: at(60), End: at(60), Price: rat(1, 1)}}
	if _, err := Settle(bad, nil, nil); err == nil {
		t.Fatal("want error for empty interval")
	}
	overlap := []Rate{
		{Start: at(0), End: at(60), Price: rat(1, 1)},
		{Start: at(30), End: at(90), Price: rat(1, 1)},
	}
	if _, err := Settle(overlap, nil, nil); err == nil {
		t.Fatal("want error for overlapping intervals")
	}
}
