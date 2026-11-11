package netsec

import (
	"math"
	"testing"
)

func TestIncDecSaturate(t *testing.T) {
	if inc(math.MaxInt64) != math.MaxInt64 {
		t.Fatalf("inc must saturate at MaxInt64")
	}
	if dec(math.MinInt64) != math.MinInt64 {
		t.Fatalf("dec must saturate at MinInt64")
	}
	if inc(1) != 2 || dec(1) != 0 {
		t.Fatalf("ordinary inc/dec broken")
	}
}

func TestMulCap(t *testing.T) {
	if got := mulCap(3, 4, 100); got != 12 {
		t.Fatalf("3*4 = %d, want 12", got)
	}
	if got := mulCap(30, 40, 100); got != 100 {
		t.Fatalf("product over cap must saturate, got %d", got)
	}
	// Would overflow a plain int64 multiply.
	if got := mulCap(math.MaxInt64, math.MaxInt64, 1000); got != 1000 {
		t.Fatalf("huge factors must saturate, got %d", got)
	}
	if got := mulCap(0, 5, 100); got != 0 {
		t.Fatalf("zero factor = %d, want 0", got)
	}
}

func TestWindowWidth(t *testing.T) {
	cases := []struct {
		lo, hi, want int64
	}{
		{0, 0, 1},
		{2, 4, 3},
		{math.MinInt64, math.MaxInt64, math.MaxInt64},
		{math.MinInt64, math.MaxInt64 - 1, math.MaxInt64},
		{math.MinInt64 + 1, math.MaxInt64, math.MaxInt64},
	}
	for _, c := range cases {
		if got := windowWidth(c.lo, c.hi); got != c.want {
			t.Fatalf("windowWidth(%d,%d) = %d, want %d", c.lo, c.hi, got, c.want)
		}
	}
}

func TestExtremeBoundsDoNotPanic(t *testing.T) {
	events := []Event{
		{ID: "u1", Feed: "f", Seq: 1, Kind: KindUse, Cred: "c", Lo: math.MinInt64, Hi: math.MaxInt64},
		{ID: "r1", Feed: "g", Seq: 1, Kind: KindRevoke, Cred: "c", Lo: math.MinInt64, Hi: math.MaxInt64},
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	// Enumeration is skipped by the domain-size cap, but the analytic
	// verdict must still be produced without overflow.
	p := rep.Pairs[0]
	if p.Verdict != VerdictStraddle || !p.EnumSkipped {
		t.Fatalf("extreme windows: verdict=%s skipped=%v, want straddle+skipped",
			p.Verdict, p.EnumSkipped)
	}
}

func TestExtremeBoundsOnOneFeed(t *testing.T) {
	events := []Event{
		{ID: "u1", Feed: "f", Seq: 1, Kind: KindUse, Cred: "c", Lo: math.MaxInt64 - 1, Hi: math.MaxInt64},
		{ID: "r1", Feed: "f", Seq: 2, Kind: KindRevoke, Cred: "c", Lo: math.MaxInt64 - 1, Hi: math.MaxInt64},
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	// Strictly ordered by seq even though only two top ticks are available.
	if p := rep.Pairs[0]; p.Verdict != VerdictBefore ||
		p.NarrowedUse.Hi != math.MaxInt64-1 ||
		p.NarrowedRevoke.Lo != math.MaxInt64 {
		t.Fatalf("extreme same-feed narrowing wrong: %+v", p)
	}
}
