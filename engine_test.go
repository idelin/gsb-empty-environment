package fxacct

import (
	"math/big"
	"testing"
)

func ratEq(t *testing.T, got *big.Rat, want string) {
	t.Helper()
	if got.Cmp(MustRat(want)) != 0 {
		t.Fatalf("got %s, want %s", got.RatString(), want)
	}
}

func eurToUSDEngine() Engine {
	return Engine{Steps: []Step{
		Convert{From: "EUR", To: "USD", Rate: MustRat("10773/10000")},
		Round{MinorUnit: 2, Mode: RoundHalfAwayFromZero},
	}}
}

// Intermediate values must stay exact fractions; rounding happens
// only at the Round node.
func TestNoPrematureRounding(t *testing.T) {
	eng := Engine{Steps: []Step{
		Convert{From: "USD", To: "EUR", Rate: MustRat("1/3")},
		Round{MinorUnit: 2, Mode: RoundHalfAwayFromZero},
	}}
	cmp, err := eng.Compare([]LineItem{{ID: "a", Amount: MustRat("1/100"), Currency: "USD"}})
	if err != nil {
		t.Fatal(err)
	}
	// After conversion the value is exactly 1/300, not 0.33 or 0.0033.
	ratEq(t, cmp.ItemEvents[0].Out, "1/300")
	// Only the Round node quantizes: 1/300 -> 0 with delta -1/300.
	ratEq(t, cmp.ItemEvents[1].Out, "0")
	ratEq(t, cmp.ItemEvents[1].Delta, "-1/300")
	ratEq(t, cmp.PerItemTotal, "0")
}

// Per-item rounding and aggregate rounding of the same bill diverge;
// the difference must be reported exactly.
func TestPerItemVsAggregate(t *testing.T) {
	items := []LineItem{
		{ID: "micro-1", Amount: MustRat("4/100"), Currency: "EUR"},
		{ID: "micro-2", Amount: MustRat("4/100"), Currency: "EUR"},
	}
	cmp, err := eurToUSDEngine().Compare(items)
	if err != nil {
		t.Fatal(err)
	}
	// Each 0.04 EUR -> 0.043092 USD -> rounds down to 0.04.
	ratEq(t, cmp.PerItemTotal, "2/25")
	// Sum 0.08 EUR -> 0.086184 USD -> rounds up to 0.09.
	ratEq(t, cmp.AggregateTotal, "9/100")
	ratEq(t, cmp.Difference, "-1/100")
	// The whole difference is explained by the rounding node.
	if len(cmp.StepPLs) != 1 {
		t.Fatalf("want 1 rounding node, got %d", len(cmp.StepPLs))
	}
	pl := cmp.StepPLs[0]
	ratEq(t, pl.PerItemDelta, "-6184/1000000")
	ratEq(t, pl.AggregateDelta, "3816/1000000")
	// Identity: difference = per-item deltas - aggregate delta.
	got := new(big.Rat).Sub(pl.PerItemDelta, pl.AggregateDelta)
	ratEq(t, got, "-1/100")
}

// A reversal exactly cancels its original in both modes.
func TestReversalNetsToZero(t *testing.T) {
	orig := LineItem{ID: "inv-1", Amount: MustRat("5"), Currency: "EUR"}
	items := []LineItem{orig, orig.Reverse("inv-1-void")}
	cmp, err := eurToUSDEngine().Compare(items)
	if err != nil {
		t.Fatal(err)
	}
	ratEq(t, cmp.PerItemTotal, "0")
	ratEq(t, cmp.AggregateTotal, "0")
	ratEq(t, cmp.Difference, "0")
	// The reversal line is linked to its origin for the audit trail.
	if !items[1].Reversal() || items[1].ReversalOf != "inv-1" {
		t.Fatalf("reversal link broken: %+v", items[1])
	}
	// Both rounding deltas are exact opposites.
	ratEq(t, cmp.ItemEvents[1].Delta, "7/2000")
	ratEq(t, cmp.ItemEvents[3].Delta, "-7/2000")
}

// Multi-hop conversion stays exact: rate 3/2 then 2/3 is identity.
func TestMultiHopExactness(t *testing.T) {
	eng := Engine{Steps: []Step{
		Convert{From: "A", To: "B", Rate: MustRat("3/2")},
		Convert{From: "B", To: "C", Rate: MustRat("2/3")},
		Round{MinorUnit: 2, Mode: RoundHalfAwayFromZero},
	}}
	cmp, err := eng.Compare([]LineItem{{ID: "x", Amount: MustRat("7/100"), Currency: "A"}})
	if err != nil {
		t.Fatal(err)
	}
	ratEq(t, cmp.ItemEvents[1].Out, "7/100")
	ratEq(t, cmp.ItemEvents[2].Delta, "0")
	ratEq(t, cmp.PerItemTotal, "7/100")
}

func TestRoundModes(t *testing.T) {
	cases := []struct {
		in   string
		mode RoundMode
		want string
	}{
		{"1/200", RoundHalfAwayFromZero, "1/100"}, // 0.005 -> 0.01
		{"1/200", RoundHalfEven, "0"},             // 0.005 -> 0.00 (even)
		{"3/200", RoundHalfEven, "2/100"},         // 0.015 -> 0.02 (even)
		{"-1/200", RoundHalfAwayFromZero, "-1/100"},
		{"-1/200", RoundHalfEven, "0"},
		{"-3/200", RoundHalfEven, "-2/100"},
	}
	for _, tc := range cases {
		step := Round{MinorUnit: 2, Mode: tc.mode}
		out, _, err := step.Apply(Money{Value: MustRat(tc.in), Currency: "USD"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Value.Cmp(MustRat(tc.want)) != 0 {
			t.Errorf("round %s (%s): got %s, want %s", tc.in, tc.mode, out.Value.RatString(), tc.want)
		}
	}
}

func TestMixedCurrencyRejected(t *testing.T) {
	items := []LineItem{
		{ID: "a", Amount: MustRat("1"), Currency: "EUR"},
		{ID: "b", Amount: MustRat("1"), Currency: "USD"},
	}
	if _, err := eurToUSDEngine().Compare(items); err == nil {
		t.Fatal("expected mixed-currency error")
	}
}

func TestEmptyBillRejected(t *testing.T) {
	if _, err := eurToUSDEngine().Compare(nil); err == nil {
		t.Fatal("expected empty-bill error")
	}
}

func TestDecimalString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1/3", "0.333333333333…"},
		{"1/4", "0.25"},
		{"-1/2", "-0.5"},
		{"7", "7"},
		{"10773/10000", "1.0773"},
	}
	for _, tc := range cases {
		if got := DecimalString(MustRat(tc.in), 12); got != tc.want {
			t.Errorf("DecimalString(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
