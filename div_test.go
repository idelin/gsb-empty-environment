package decimal

import (
	"math/big"
	"testing"
)

func TestDivExactTable(t *testing.T) {
	cases := []struct {
		a, b      string
		maxScale  uint32
		wantExact string // "" means Exact must be nil
	}{
		{"1", "2", 10, "0.5"},
		{"1", "40", 10, "0.025"},
		{"1", "3", 100, ""},   // non-terminating
		{"1", "7", 100, ""},   // non-terminating
		{"1", "8192", 10, ""}, // needs 13 digits: budget insufficient
		{"1", "8192", 12, ""}, // still insufficient
		{"1", "8192", 13, "0.0001220703125"},
		{"1", "8192", 100, "0.0001220703125"},
		{"-7", "2", 5, "-3.5"},
		{"1", "0.001", 5, "1000"},
		{"2.5", "0.05", 5, "50"},
		{"1e4", "1e-2", 5, "1000000"},
		{"0", "3", 5, "0"},
		{"22", "7", 10, ""},
		{"-1", "6", 10, ""},
		{"3", "6", 10, "0.5"},
		{"123.456", "0.008", 10, "15432"},
	}
	for _, tc := range cases {
		ctx := Context{Precision: 20, Mode: HalfEven, MaxScale: tc.maxScale}
		o, err := ctx.Div(mustParse(t, tc.a), mustParse(t, tc.b))
		if err != nil {
			t.Fatalf("Div(%s, %s): %v", tc.a, tc.b, err)
		}
		if tc.wantExact == "" {
			if o.Exact != nil {
				t.Errorf("Div(%s, %s, MaxScale=%d): Exact = %s, want nil",
					tc.a, tc.b, tc.maxScale, o.Exact)
			}
		} else {
			if o.Exact == nil {
				t.Errorf("Div(%s, %s, MaxScale=%d): Exact nil, want %s",
					tc.a, tc.b, tc.maxScale, tc.wantExact)
			} else if o.Exact.String() != tc.wantExact {
				t.Errorf("Div(%s, %s, MaxScale=%d): Exact = %s, want %s",
					tc.a, tc.b, tc.maxScale, o.Exact, tc.wantExact)
			}
		}
	}
}

// TestDivBudgetNoFakeExact ensures an over-budget quotient is never
// reported as a zero-padded "exact" value.
func TestDivBudgetNoFakeExact(t *testing.T) {
	ctx := Context{Precision: 10, Mode: HalfEven, MaxScale: 10}
	o, err := ctx.Div(mustParse(t, "1"), mustParse(t, "8192"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Exact != nil {
		t.Fatalf("1/8192 with MaxScale=10 reported Exact = %s", o.Exact)
	}
	// The rounded value must be the true rounding, not a truncation.
	if got, want := o.Rounded.String(), "0.0001220703"; got != want {
		t.Fatalf("Rounded = %s, want %s", got, want)
	}
	// 1/3 can never be exact at any scale.
	for _, ms := range []uint32{0, 5, 50, 1000} {
		ctx := Context{Precision: 4, Mode: ToZero, MaxScale: ms}
		o, err := ctx.Div(mustParse(t, "1"), mustParse(t, "3"))
		if err != nil {
			t.Fatal(err)
		}
		if o.Exact != nil {
			t.Fatalf("1/3 with MaxScale=%d reported Exact = %s", ms, o.Exact)
		}
	}
}

func TestDivRoundingModes(t *testing.T) {
	cases := []struct {
		a, b  string
		scale uint32
		want  map[RoundingMode]string
	}{
		{"-7", "2", 0, map[RoundingMode]string{ToZero: "-3", ToPosInf: "-3", ToNegInf: "-4", HalfEven: "-4"}},
		{"-5", "2", 0, map[RoundingMode]string{ToZero: "-2", ToPosInf: "-2", ToNegInf: "-3", HalfEven: "-2"}},
		{"5", "2", 0, map[RoundingMode]string{ToZero: "2", ToPosInf: "3", ToNegInf: "2", HalfEven: "2"}},
		{"1", "3", 4, map[RoundingMode]string{ToZero: "0.3333", ToPosInf: "0.3334", ToNegInf: "0.3333", HalfEven: "0.3333"}},
		{"-1", "3", 4, map[RoundingMode]string{ToZero: "-0.3333", ToPosInf: "-0.3333", ToNegInf: "-0.3334", HalfEven: "-0.3333"}},
		{"2", "3", 0, map[RoundingMode]string{ToZero: "0", ToPosInf: "1", ToNegInf: "0", HalfEven: "1"}},
		{"1", "6", 2, map[RoundingMode]string{ToZero: "0.16", ToPosInf: "0.17", ToNegInf: "0.16", HalfEven: "0.17"}},
		{"-1", "6", 2, map[RoundingMode]string{ToZero: "-0.16", ToPosInf: "-0.16", ToNegInf: "-0.17", HalfEven: "-0.17"}},
	}
	modes := []RoundingMode{ToZero, ToPosInf, ToNegInf, HalfEven}
	for _, tc := range cases {
		for _, m := range modes {
			ctx := Context{Precision: tc.scale, Mode: m, MaxScale: 0}
			o, err := ctx.Div(mustParse(t, tc.a), mustParse(t, tc.b))
			if err != nil {
				t.Fatalf("Div(%s, %s): %v", tc.a, tc.b, err)
			}
			if got := o.Rounded.String(); got != tc.want[m] {
				t.Errorf("Div(%s, %s) round %d scale %d = %s, want %s",
					tc.a, tc.b, m, tc.scale, got, tc.want[m])
			}
		}
	}
}

func TestDivByZero(t *testing.T) {
	ctx := Context{Precision: 4, Mode: HalfEven, MaxScale: 10}
	for _, z := range []string{"0", "0.00", "-0", "0e5"} {
		if _, err := ctx.Div(mustParse(t, "1"), mustParse(t, z)); err != ErrDivisionByZero {
			t.Errorf("Div(1, %q): err = %v, want ErrDivisionByZero", z, err)
		}
	}
	if _, err := ctx.Div(Zero, Zero); err != ErrDivisionByZero {
		t.Errorf("Div(0, 0): err = %v, want ErrDivisionByZero", err)
	}
}

// TestDivHugeExponents checks division with very large exponent gaps.
func TestDivHugeExponents(t *testing.T) {
	// 1e20000 / 3 is non-terminating; rounded value must match the
	// rational reference.
	ctx := Context{Precision: 10, Mode: HalfEven, MaxScale: 100}
	a := New(1, 20000)
	o, err := ctx.Div(a, New(3, 0))
	if err != nil {
		t.Fatal(err)
	}
	if o.Exact != nil {
		t.Fatalf("1e20000/3 reported exact")
	}
	want := roundRatRef(new(big.Rat).Quo(a.Rat(), big.NewRat(3, 1)), 10, HalfEven)
	if got := o.Rounded.Rat(); got.Cmp(want) != 0 {
		t.Fatalf("Rounded = %s, want %s", got, want)
	}
	// 2.5e-10000 / 5e-20000 = 5e9999 exactly, scale 0.
	o, err = ctx.Div(mustParse(t, "2.5e-10000"), mustParse(t, "5e-20000"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Exact == nil || o.Exact.String() != "5"+zeros(9999) {
		s := "<nil>"
		if o.Exact != nil {
			s = o.Exact.String()
		}
		t.Fatalf("Exact = %s, want 5e9999", s)
	}
	// 1e-20000 / 8 terminates but needs 20003 digits: over budget.
	o, err = ctx.Div(New(1, -20000), New(8, 0))
	if err != nil {
		t.Fatal(err)
	}
	if o.Exact != nil {
		t.Fatalf("1e-20000/8 with MaxScale=100 reported exact")
	}
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}
