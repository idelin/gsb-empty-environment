package settle

import (
	"math/big"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func at(hours int) time.Time { return epoch.Add(time.Duration(hours) * time.Hour) }

// twoSegBook builds [0h,4h) @ rateA and [4h,+inf) @ rateB.
func twoSegBook(t *testing.T, rateA, rateB string) *Book {
	t.Helper()
	b, err := NewBook([]Segment{
		{Start: at(0), End: at(4), Rate: rat(rateA)},
		{Start: at(4), Rate: rat(rateB)},
	})
	if err != nil {
		t.Fatalf("NewBook: %v", err)
	}
	return b
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.125", "0.12"}, // tie, 2 is even
		{"0.135", "0.14"}, // tie, 4 is even
		{"0.025", "0.02"},
		{"0.035", "0.04"},
		{"-0.125", "-0.12"},
		{"-0.135", "-0.14"},
		{"0.12499999", "0.12"},
		{"0.12500001", "0.13"},
		{"1/3", "0.33"},
		{"2/3", "0.67"},
		{"-1/3", "-0.33"},
		{"0", "0.00"},
	}
	for _, c := range cases {
		got := Money(RoundToCents(rat(c.in)))
		if got != c.want {
			t.Errorf("RoundToCents(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestInstantEndpointBoundaries(t *testing.T) {
	b := twoSegBook(t, "2", "3")
	// Event exactly at the boundary belongs to the right (later) segment.
	st, err := b.Settle([]Usage{Instant{At: at(4), Qty: rat("1")}})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := Money(st.Total); got != "3.00" {
		t.Errorf("boundary event billed %s, want 3.00 (right segment)", got)
	}
	// One nanosecond before the boundary belongs to the left segment.
	st, err = b.Settle([]Usage{Instant{At: at(4).Add(-time.Nanosecond), Qty: rat("1")}})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := Money(st.Total); got != "2.00" {
		t.Errorf("pre-boundary event billed %s, want 2.00 (left segment)", got)
	}
	// Event exactly at book start belongs to the first segment.
	st, err = b.Settle([]Usage{Instant{At: at(0), Qty: rat("1")}})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := Money(st.Total); got != "2.00" {
		t.Errorf("start event billed %s, want 2.00", got)
	}
}

func TestLinearApportionedByDuration(t *testing.T) {
	b := twoSegBook(t, "2", "3")
	// 10 units spread uniformly over [0h,10h): 4/10 to seg A, 6/10 to seg B.
	u, err := NewLinear(at(0), at(10), rat("10"))
	if err != nil {
		t.Fatalf("NewLinear: %v", err)
	}
	st, err := b.Settle([]Usage{u})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(st.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(st.Items))
	}
	if st.Items[0].Quantity.Cmp(rat("4")) != 0 {
		t.Errorf("seg A qty = %s, want 4", st.Items[0].Quantity)
	}
	if st.Items[1].Quantity.Cmp(rat("6")) != 0 {
		t.Errorf("seg B qty = %s, want 6", st.Items[1].Quantity)
	}
	if st.ExactTotal.Cmp(rat("26")) != 0 {
		t.Errorf("exact total = %s, want 26", st.ExactTotal)
	}
	if got := Money(st.Total); got != "26.00" {
		t.Errorf("total = %s, want 26.00", got)
	}
}

func TestLinearPartitionOfUnity(t *testing.T) {
	// Awkward durations and a prime quantity: allocations must sum back
	// to the exact total with no rational drift.
	b, err := NewBook([]Segment{
		{Start: at(0), End: at(1).Add(7 * time.Minute), Rate: rat("0.11")},
		{Start: at(1).Add(7 * time.Minute), End: at(3).Add(23 * time.Second), Rate: rat("0.07")},
		{Start: at(3).Add(23 * time.Second), Rate: rat("0.03")},
	})
	if err != nil {
		t.Fatalf("NewBook: %v", err)
	}
	u, err := NewLinear(at(0).Add(13*time.Second), at(5).Add(59*time.Second), rat("7"))
	if err != nil {
		t.Fatalf("NewLinear: %v", err)
	}
	st, err := b.Settle([]Usage{u})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	sum := new(big.Rat)
	for _, it := range st.Items {
		sum.Add(sum, it.Quantity)
	}
	if sum.Cmp(rat("7")) != 0 {
		t.Errorf("allocated quantities sum to %s, want exactly 7", sum)
	}
}

func TestZeroDuration(t *testing.T) {
	if _, err := NewLinear(at(1), at(1), rat("5")); err == nil {
		t.Error("zero-duration linear usage accepted, want error")
	}
	if _, err := NewLinear(at(2), at(1), rat("5")); err == nil {
		t.Error("negative-duration linear usage accepted, want error")
	}
	if _, err := NewBook([]Segment{{Start: at(1), End: at(1), Rate: rat("1")}}); err == nil {
		t.Error("zero-duration segment accepted, want error")
	}
	// A linear usage adjacent to a boundary contributes nothing across it.
	b := twoSegBook(t, "2", "3")
	u, _ := NewLinear(at(0), at(4), rat("8"))
	st, err := b.Settle([]Usage{u})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(st.Items) != 1 || st.Items[0].Quantity.Cmp(rat("8")) != 0 {
		t.Errorf("usage ending exactly at boundary leaked into next segment: %+v", st.Items)
	}
}

func TestRoundingOnlyAtEnd(t *testing.T) {
	// Two items of 0.015 each: per-item half-even rounding would give
	// 0.02 + 0.02 = 0.04, but rounding the exact total gives 0.03.
	b, err := NewBook([]Segment{{Start: at(0), Rate: rat("0.015")}})
	if err != nil {
		t.Fatalf("NewBook: %v", err)
	}
	st, err := b.Settle([]Usage{
		Instant{At: at(0), Qty: rat("1")},
		Instant{At: at(1), Qty: rat("1")},
	})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := Money(st.Total); got != "0.03" {
		t.Errorf("total = %s, want 0.03 (rounded once at the end)", got)
	}
}

func TestRetroactiveRevisionDelta(t *testing.T) {
	b := twoSegBook(t, "2", "3")
	usages := []Usage{
		Instant{At: at(1), Qty: rat("5")}, // 5 @ 2 = 10
		Instant{At: at(5), Qty: rat("7")}, // 7 @ 3 = 21
	}
	st, err := b.Settle(usages)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if got := Money(st.Total); got != "31.00" {
		t.Fatalf("initial total = %s, want 31.00", got)
	}

	// Retroactively raise the first segment's rate 2 -> 5.
	d, err := b.Revise(Segment{Start: at(0), End: at(4), Rate: rat("5")}, usages)
	if err != nil {
		t.Fatalf("Revise: %v", err)
	}
	if d.Quantity.Cmp(rat("5")) != 0 {
		t.Errorf("delta quantity = %s, want 5", d.Quantity)
	}
	if d.ExactAmount.Cmp(rat("15")) != 0 {
		t.Errorf("exact delta = %s, want 15 (5 * (5-2))", d.ExactAmount)
	}
	if got := Money(d.Amount); got != "15.00" {
		t.Errorf("delta = %s, want 15.00", got)
	}

	// The book now settles to old total + delta; the delta itself is the
	// only output of the revision, not a restated total.
	st2, err := b.Settle(usages)
	if err != nil {
		t.Fatalf("Settle after revise: %v", err)
	}
	want := new(big.Rat).Add(st.ExactTotal, d.ExactAmount)
	if st2.ExactTotal.Cmp(want) != 0 {
		t.Errorf("post-revision exact total = %s, want %s", st2.ExactTotal, want)
	}
}

func TestRepeatedRevisions(t *testing.T) {
	b := twoSegBook(t, "2", "3")
	u, err := NewLinear(at(0), at(8), rat("8")) // 4 units per segment
	if err != nil {
		t.Fatalf("NewLinear: %v", err)
	}
	usages := []Usage{u}

	initial, err := b.Settle(usages)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}

	rates := []string{"5", "1", "7"} // successive rates for the first segment
	cumulative := new(big.Rat)
	for _, r := range rates {
		d, err := b.Revise(Segment{Start: at(0), End: at(4), Rate: rat(r)}, usages)
		if err != nil {
			t.Fatalf("Revise to %s: %v", r, err)
		}
		cumulative.Add(cumulative, d.ExactAmount)
	}

	final, err := b.Settle(usages)
	if err != nil {
		t.Fatalf("final Settle: %v", err)
	}
	// The sum of all deltas must reconcile exactly with final - initial.
	want := new(big.Rat).Sub(final.ExactTotal, initial.ExactTotal)
	if cumulative.Cmp(want) != 0 {
		t.Errorf("cumulative deltas = %s, want %s (final %s - initial %s)",
			cumulative, want, final.ExactTotal, initial.ExactTotal)
	}
	// Sanity: 4 units * (7 - 2) = 20.
	if cumulative.Cmp(rat("20")) != 0 {
		t.Errorf("cumulative deltas = %s, want 20", cumulative)
	}
	// Revising a non-existent interval must fail.
	if _, err := b.Revise(Segment{Start: at(0), End: at(5), Rate: rat("9")}, usages); err == nil {
		t.Error("revise of mismatched interval accepted, want error")
	}
}

func TestUncoveredUsage(t *testing.T) {
	closed, err := NewBook([]Segment{{Start: at(0), End: at(4), Rate: rat("1")}})
	if err != nil {
		t.Fatalf("NewBook: %v", err)
	}
	if _, err := closed.Settle([]Usage{Instant{At: at(5), Qty: rat("1")}}); err == nil {
		t.Error("instant outside coverage accepted, want error")
	}
	u, _ := NewLinear(at(3), at(5), rat("2"))
	if _, err := closed.Settle([]Usage{u}); err == nil {
		t.Error("linear usage past coverage accepted, want error")
	}
	// Open-ended final segment covers everything after its start.
	open := twoSegBook(t, "2", "3")
	if _, err := open.Settle([]Usage{Instant{At: at(100000), Qty: rat("1")}}); err != nil {
		t.Errorf("open-ended book rejected far-future usage: %v", err)
	}
}

func TestInvalidBook(t *testing.T) {
	if _, err := NewBook(nil); err == nil {
		t.Error("empty book accepted, want error")
	}
	if _, err := NewBook([]Segment{{Start: at(0), Rate: nil}}); err == nil {
		t.Error("nil rate accepted, want error")
	}
	if _, err := NewBook([]Segment{
		{Start: at(0), End: at(4), Rate: rat("1")},
		{Start: at(3), Rate: rat("1")},
	}); err == nil {
		t.Error("overlapping segments accepted, want error")
	}
	if _, err := NewBook([]Segment{
		{Start: at(4), End: at(8), Rate: rat("1")},
		{Start: at(0), Rate: rat("1")},
	}); err == nil {
		t.Error("out-of-order segments accepted, want error")
	}
}
