package interval

import (
	"math/big"
	"testing"
)

func TestFloorCeilTo(t *testing.T) {
	cases := []struct {
		r           *big.Rat
		digits      int
		floor, ceil string
	}{
		{rat(12345, 10000), 2, "1.23", "1.24"},
		{rat(-12345, 10000), 2, "-1.24", "-1.23"}, // negatives round outward
		{rat(-5, 1), 2, "-5.00", "-5.00"},         // exact values unchanged
		{rat(1, 3), 0, "0", "1"},
		{rat(-1, 3), 0, "-1", "0"},
		{rat(999, 1000), 3, "0.999", "0.999"},
	}
	for k, c := range cases {
		if got := DecimalString(FloorTo(c.r, c.digits), c.digits); got != c.floor {
			t.Errorf("case %d: floor = %s, want %s", k, got, c.floor)
		}
		if got := DecimalString(CeilTo(c.r, c.digits), c.digits); got != c.ceil {
			t.Errorf("case %d: ceil = %s, want %s", k, got, c.ceil)
		}
	}
}

// samplePoints returns rational probe points inside and around iv.
func samplePoints(iv Interval) []*big.Rat {
	pts := []*big.Rat{
		rat(0, 1), rat(1, 1), rat(-1, 1), rat(1, 1000000), rat(-1, 1000000),
	}
	if !iv.lo.IsInf() {
		lo := iv.lo.val
		pts = append(pts, lo,
			new(big.Rat).Add(lo, rat(1, 1000000)),
			new(big.Rat).Sub(lo, rat(1, 1000000)))
	}
	if !iv.hi.IsInf() {
		hi := iv.hi.val
		pts = append(pts, hi,
			new(big.Rat).Add(hi, rat(1, 1000000)),
			new(big.Rat).Sub(hi, rat(1, 1000000)))
	}
	if !iv.lo.IsInf() && !iv.hi.IsInf() {
		mid := new(big.Rat).Add(iv.lo.val, iv.hi.val)
		mid.Quo(mid, rat(2, 1))
		pts = append(pts, mid)
	}
	return pts
}

func isDecimalMultiple(r *big.Rat, digits int) bool {
	n := new(big.Int).Mul(r.Num(), scaleInt(digits))
	return new(big.Int).Rem(n, r.Denom()).Sign() == 0
}

func TestEncloseContainmentAndBoundaries(t *testing.T) {
	ivs := []Interval{
		Closed(rat(1, 3), rat(2, 3)),
		Closed(rat(-7, 3), rat(-1, 3)),                   // negative range
		Closed(rat(-1, 3), rat(1, 3)),                    // across zero
		Make(Fin(rat(1, 7)), Fin(rat(9, 7)), true, true), // open both ends
		Make(Fin(rat(-5, 4)), Fin(rat(-3, 4)), true, false),
		Make(NegInf(), Fin(rat(1, 9)), true, false),  // infinite left
		Make(Fin(rat(-1, 9)), PosInf(), false, true), // infinite right
		All(),
		Point(rat(12345, 10000)), // single point
		Point(rat(-1, 1000000)),
		Empty(),
	}
	for _, iv := range ivs {
		for digits := 0; digits <= 4; digits++ {
			enc := iv.Enclose(digits)
			if iv.IsEmpty() {
				if !enc.IsEmpty() {
					t.Errorf("empty must stay empty, got %v", enc)
				}
				continue
			}
			if enc.IsEmpty() {
				t.Errorf("Enclose(%v, %d) became empty", iv, digits)
				continue
			}
			// Boundary check: rounded endpoints bracket the exact ones.
			if !iv.lo.IsInf() && cmpExt(enc.lo, iv.lo) > 0 {
				t.Errorf("%v: enclosed lo %v above exact lo", iv, enc.lo)
			}
			if !iv.hi.IsInf() && cmpExt(enc.hi, iv.hi) < 0 {
				t.Errorf("%v: enclosed hi %v below exact hi", iv, enc.hi)
			}
			// Openness flags must not break containment at equal bounds.
			if cmpExt(enc.lo, iv.lo) == 0 && !iv.loOpen && enc.loOpen {
				t.Errorf("%v: lo flag broke containment", iv)
			}
			if cmpExt(enc.hi, iv.hi) == 0 && !iv.hiOpen && enc.hiOpen {
				t.Errorf("%v: hi flag broke containment", iv)
			}
			// Rounded endpoints are exact decimals with <= digits places.
			if !enc.lo.IsInf() && !isDecimalMultiple(enc.lo.val, digits) {
				t.Errorf("%v: lo %v not a %d-digit decimal", iv, enc.lo.val, digits)
			}
			if !enc.hi.IsInf() && !isDecimalMultiple(enc.hi.val, digits) {
				t.Errorf("%v: hi %v not a %d-digit decimal", iv, enc.hi.val, digits)
			}
			// Rational point containment: every point of iv stays inside.
			for _, p := range samplePoints(iv) {
				if iv.Contains(p) && !enc.Contains(p) {
					t.Errorf("Enclose(%v, %d) = %v lost point %v", iv, digits, enc, p)
				}
			}
		}
	}
}

func TestEnclosePointNotEmpty(t *testing.T) {
	p := Point(rat(12345, 10000)) // 1.2345
	enc := p.Enclose(2)
	if enc.IsEmpty() {
		t.Fatal("enclosure of a point must not be empty")
	}
	if !enc.Contains(rat(12345, 10000)) {
		t.Error("enclosure must contain the exact point")
	}
	if got := DecimalString(enc.lo.val, 2); got != "1.23" {
		t.Errorf("lo = %s, want 1.23", got)
	}
	if got := DecimalString(enc.hi.val, 2); got != "1.24" {
		t.Errorf("hi = %s, want 1.24", got)
	}
	// Exact decimal point: enclosure collapses back to the point.
	enc = Point(rat(25, 10)).Enclose(1)
	if !enc.IsPoint() || !enc.Contains(rat(25, 10)) {
		t.Errorf("exact decimal point enclosure = %v, want {2.5}", enc)
	}
}

func TestEncloseOpenFlagsPreserved(t *testing.T) {
	// (0.5, 1.5) at 0 digits: (0, 2) — flags kept, containment holds.
	iv := Make(Fin(rat(1, 2)), Fin(rat(3, 2)), true, true)
	enc := iv.Enclose(0)
	if !enc.loOpen || !enc.hiOpen {
		t.Errorf("flags should be preserved: %v", enc)
	}
	for _, p := range []*big.Rat{rat(500001, 1000000), rat(1499999, 1000000), rat(1, 1)} {
		if !enc.Contains(p) {
			t.Errorf("%v must contain %v", enc, p)
		}
	}
	// Infinite endpoints stay infinite and open.
	enc = Make(NegInf(), Fin(rat(1, 3)), true, true).Enclose(2)
	if !enc.lo.IsInf() || !enc.loOpen {
		t.Errorf("infinite endpoint must survive rounding: %v", enc)
	}
	if got := DecimalString(enc.hi.val, 2); got != "0.34" {
		t.Errorf("hi = %s, want 0.34", got)
	}
}
