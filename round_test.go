package decimal

import "testing"

// TestRoundTable covers negative half values and ties under every mode.
func TestRoundTable(t *testing.T) {
	cases := []struct {
		in    string
		scale uint32
		want  map[RoundingMode]string
	}{
		{"-2.5", 0, map[RoundingMode]string{ToZero: "-2", ToPosInf: "-2", ToNegInf: "-3", HalfEven: "-2"}},
		{"-1.5", 0, map[RoundingMode]string{ToZero: "-1", ToPosInf: "-1", ToNegInf: "-2", HalfEven: "-2"}},
		{"-0.5", 0, map[RoundingMode]string{ToZero: "0", ToPosInf: "0", ToNegInf: "-1", HalfEven: "0"}},
		{"0.5", 0, map[RoundingMode]string{ToZero: "0", ToPosInf: "1", ToNegInf: "0", HalfEven: "0"}},
		{"1.5", 0, map[RoundingMode]string{ToZero: "1", ToPosInf: "2", ToNegInf: "1", HalfEven: "2"}},
		{"2.5", 0, map[RoundingMode]string{ToZero: "2", ToPosInf: "3", ToNegInf: "2", HalfEven: "2"}},
		{"3.5", 0, map[RoundingMode]string{ToZero: "3", ToPosInf: "4", ToNegInf: "3", HalfEven: "4"}},
		{"-0.05", 1, map[RoundingMode]string{ToZero: "0", ToPosInf: "0", ToNegInf: "-0.1", HalfEven: "0"}},
		{"-0.15", 1, map[RoundingMode]string{ToZero: "-0.1", ToPosInf: "-0.1", ToNegInf: "-0.2", HalfEven: "-0.2"}},
		{"-2.25", 1, map[RoundingMode]string{ToZero: "-2.2", ToPosInf: "-2.2", ToNegInf: "-2.3", HalfEven: "-2.2"}},
		{"-2.35", 1, map[RoundingMode]string{ToZero: "-2.3", ToPosInf: "-2.3", ToNegInf: "-2.4", HalfEven: "-2.4"}},
		{"0.125", 2, map[RoundingMode]string{ToZero: "0.12", ToPosInf: "0.13", ToNegInf: "0.12", HalfEven: "0.12"}},
		{"0.375", 2, map[RoundingMode]string{ToZero: "0.37", ToPosInf: "0.38", ToNegInf: "0.37", HalfEven: "0.38"}},
		{"-0.001", 2, map[RoundingMode]string{ToZero: "0", ToPosInf: "0", ToNegInf: "-0.01", HalfEven: "0"}},
		{"0.0049", 2, map[RoundingMode]string{ToZero: "0", ToPosInf: "0.01", ToNegInf: "0", HalfEven: "0"}},
		{"9.999", 2, map[RoundingMode]string{ToZero: "9.99", ToPosInf: "10.00", ToNegInf: "9.99", HalfEven: "10.00"}},
		{"-9.999", 2, map[RoundingMode]string{ToZero: "-9.99", ToPosInf: "-9.99", ToNegInf: "-10.00", HalfEven: "-10.00"}},
	}
	modes := []RoundingMode{ToZero, ToPosInf, ToNegInf, HalfEven}
	for _, tc := range cases {
		d := mustParse(t, tc.in)
		for _, m := range modes {
			got := d.Round(tc.scale, m).String()
			if got != tc.want[m] {
				t.Errorf("Round(%s, %d, %d) = %s, want %s", tc.in, tc.scale, m, got, tc.want[m])
			}
		}
	}
}

// TestRoundAgainstRatRef cross-checks Round against the independent
// rational reference on targeted values, including negative halves.
func TestRoundAgainstRatRef(t *testing.T) {
	modes := []RoundingMode{ToZero, ToPosInf, ToNegInf, HalfEven}
	vals := []string{
		"-2.5", "-1.5", "-0.5", "0.5", "-0.05", "-0.15", "-2.25", "-2.35",
		"0.125", "0.375", "-0.001", "123.456", "-987.654", "0.4999",
		"0.5001", "-0.5001", "2.675", "-2.675", "1.005", "-1.005",
	}
	for _, v := range vals {
		d := mustParse(t, v)
		r := d.Rat()
		for scale := uint32(0); scale <= 4; scale++ {
			for _, m := range modes {
				got := d.Round(scale, m).Rat()
				want := roundRatRef(r, scale, m)
				if got.Cmp(want) != 0 {
					t.Errorf("Round(%s, %d, %d) = %s, want %s", v, scale, m, got, want)
				}
			}
		}
	}
}

// TestRoundNeverPads verifies Round does not append zeros to imply
// precision the value does not have.
func TestRoundNeverPads(t *testing.T) {
	d := mustParse(t, "1.5")
	if got := d.Round(10, HalfEven); got.Exp() != -1 || got.String() != "1.5" {
		t.Errorf("Round(1.5, 10) = %s (exp %d), want unchanged 1.5", got, got.Exp())
	}
}
