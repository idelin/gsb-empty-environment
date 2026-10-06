package decimal_test

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"gauge/decimal"
)

// refRound is an independent reference implementation of half-even
// rounding written directly against big.Rat/big.Int, used to
// cross-check the library.
func refRound(num, den *big.Int, scale int) *big.Rat {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	t := new(big.Int).Mul(num, p)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(t, den, r)
	twice := new(big.Int).Abs(r)
	twice.Lsh(twice, 1)
	bump := false
	switch c := twice.Cmp(den); {
	case c > 0:
		bump = true
	case c == 0:
		bump = new(big.Int).Abs(q).Bit(0) == 1
	}
	if bump {
		if t.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(q, p)
}

func mustFixed(t *testing.T, v decimal.Value, scale int) string {
	t.Helper()
	s, err := v.FixedString(scale)
	if err != nil {
		t.Fatalf("FixedString(%d): %v", scale, err)
	}
	return s
}

func TestParse(t *testing.T) {
	cases := map[string]string{
		"123":     "123",
		"123.45":  "123.45",
		"-0.001":  "-0.001",
		"+7":      "7",
		"0.500":   "0.5",
		"42.":     "42",
		".5":      "0.5",
		"-0":      "0",
		"-0.0000": "0",
	}
	for in, want := range cases {
		v, err := decimal.Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got := v.String(); got != want {
			t.Errorf("Parse(%q) = %s, want %s", in, got, want)
		}
		if v.Sign() == 0 && in[0] == '-' && v.String() != "0" {
			t.Errorf("Parse(%q): negative zero leaked as %s", in, v.String())
		}
	}
	for _, bad := range []string{"", ".", "-", "1.2.3", "1e3", "0x10", "abc", "1..2", "--1"} {
		if _, err := decimal.Parse(bad); err == nil {
			t.Errorf("Parse(%q): expected error", bad)
		}
	}
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		in       string
		scale    int
		want     string
		tie      bool
		adjusted bool
	}{
		// Positive midpoints: ties resolve to the even digit.
		{"2.5", 0, "2", true, false},
		{"3.5", 0, "4", true, true},
		{"0.125", 2, "0.12", true, false},
		{"0.375", 2, "0.38", true, true},
		{"1.005", 2, "1", true, false},
		{"2.675", 2, "2.68", true, true},
		// Negative midpoints mirror the positive ones.
		{"-2.5", 0, "-2", true, false},
		{"-3.5", 0, "-4", true, true},
		{"-0.125", 2, "-0.12", true, false},
		{"-0.375", 2, "-0.38", true, true},
		// Carry propagation across the decimal point and beyond.
		{"9.995", 2, "10", true, true},
		{"0.996", 2, "1", false, true},
		{"99.95", 1, "100", true, true},
		{"-9.995", 2, "-10", true, true},
		// Negative zero is unified to zero.
		{"-0.005", 2, "0", true, false},
		{"-0.004", 2, "0", false, false},
		// Ordinary up/down rounding.
		{"0.126", 2, "0.13", false, true},
		{"0.124", 2, "0.12", false, false},
		{"-0.126", 2, "-0.13", false, true},
	}
	for _, c := range cases {
		v := decimal.MustParse(c.in)
		got, ev, err := v.Round(c.scale)
		if err != nil {
			t.Errorf("Round(%s, %d): %v", c.in, c.scale, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("Round(%s, %d) = %s, want %s", c.in, c.scale, got, c.want)
		}
		if ev.Tie != c.tie || ev.Adjusted != c.adjusted {
			t.Errorf("Round(%s, %d) evidence tie=%t adjusted=%t, want tie=%t adjusted=%t",
				c.in, c.scale, ev.Tie, ev.Adjusted, c.tie, c.adjusted)
		}
		if ev.Exact.Cmp(v) != 0 || ev.Result.Cmp(got) != 0 {
			t.Errorf("Round(%s, %d) evidence mismatch: %s", c.in, c.scale, ev)
		}
		// Truncated + Remainder must reconstruct the exact value.
		if ev.Truncated.Add(ev.Remainder).Cmp(v) != 0 {
			t.Errorf("Round(%s, %d): truncated+remainder != exact (%s)", c.in, c.scale, ev)
		}
	}
}

func TestNegativeZeroUnified(t *testing.T) {
	v := decimal.MustParse("-0.000")
	if v.Sign() != 0 || v.String() != "0" {
		t.Fatalf("Parse(-0.000) = %s (sign %d)", v, v.Sign())
	}
	if s := mustFixed(t, v, 3); s != "0.000" {
		t.Fatalf("FixedString = %q, want 0.000", s)
	}
	// Arithmetic that lands on zero from the negative side stays "0".
	neg := decimal.MustParse("-0.5")
	zero := neg.Mul(decimal.Zero())
	if zero.Sign() != 0 || zero.String() != "0" {
		t.Fatalf("-0.5 * 0 = %s", zero)
	}
	r, _, err := decimal.MustParse("-0.0001").Round(3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sign() != 0 || r.String() != "0" {
		t.Fatalf("Round(-0.0001, 3) = %s", r)
	}
}

func TestDivisionByZero(t *testing.T) {
	if _, err := decimal.NewFromInt(1).Div(decimal.Zero()); err != decimal.ErrDivisionByZero {
		t.Fatalf("Div by zero: %v", err)
	}
	if _, err := decimal.NewRational(1, 0); err != decimal.ErrDivisionByZero {
		t.Fatalf("NewRational(1, 0): %v", err)
	}
	if _, err := decimal.NewRational(0, 0); err != decimal.ErrDivisionByZero {
		t.Fatalf("NewRational(0, 0): %v", err)
	}
}

func TestConversionChain(t *testing.T) {
	// Gauge value in inches; exact rational factors inch->mm->m.
	gauge := decimal.MustParse("123.45")
	inToMM, err := decimal.NewRational(127, 5) // 25.4
	if err != nil {
		t.Fatal(err)
	}
	mmToM, err := decimal.NewRational(1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// Step-by-step chain equals the composed factor, exactly.
	step := gauge.Mul(inToMM).Mul(mmToM)
	direct := gauge.Mul(inToMM.Mul(mmToM))
	if step.Cmp(direct) != 0 {
		t.Fatalf("chain %s != composed %s", step, direct)
	}
	// Round trip back to the original unit returns the input exactly.
	back1, err := step.Div(mmToM)
	if err != nil {
		t.Fatal(err)
	}
	back2, err := back1.Div(inToMM)
	if err != nil {
		t.Fatal(err)
	}
	if back2.Cmp(gauge) != 0 {
		t.Fatalf("round trip = %s, want %s", back2, gauge)
	}
	// Rounding happens once, at the end.
	rounded, ev, err := step.Round(4)
	if err != nil {
		t.Fatal(err)
	}
	if got := rounded.String(); got != "3.1356" {
		t.Fatalf("123.45in in metres rounded to 4dp = %s, want 3.1356 (%s)", got, ev)
	}
}

func TestRandomizedReferenceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	randDecimal := func() decimal.Value {
		s := fmt.Sprintf("%d.%03d", rng.Intn(20000)-10000, rng.Intn(1000))
		return decimal.MustParse(s)
	}
	randFactor := func() decimal.Value {
		num := int64(rng.Intn(2001) - 1000)
		den := int64(rng.Intn(1000) + 1)
		if rng.Intn(2) == 0 {
			den = -den
		}
		v, err := decimal.NewRational(num, den)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for i := 0; i < 500; i++ {
		v := randDecimal()
		ref := v.Rat()
		ops := 1 + rng.Intn(6)
		for j := 0; j < ops; j++ {
			f := randFactor()
			if rng.Intn(2) == 0 || f.IsZero() {
				v = v.Mul(f)
				ref.Mul(ref, f.Rat())
			} else {
				var err error
				v, err = v.Div(f)
				if err != nil {
					t.Fatal(err)
				}
				ref.Quo(ref, f.Rat())
			}
		}
		if v.Rat().Cmp(ref) != 0 {
			t.Fatalf("iter %d: exact chain %s != reference %s", i, v.Rat(), ref)
		}
		scale := rng.Intn(7)
		rounded, _, err := v.Round(scale)
		if err != nil {
			t.Fatal(err)
		}
		want := refRound(ref.Num(), ref.Denom(), scale)
		if rounded.Rat().Cmp(want) != 0 {
			t.Fatalf("iter %d: Round(%s, %d) = %s, reference %s",
				i, v, scale, rounded.Rat(), want)
		}
	}
}

func TestFixedString(t *testing.T) {
	v := decimal.MustParse("3.13563")
	if _, err := v.FixedString(4); err == nil {
		t.Fatal("expected error: 3.13563 not representable at scale 4")
	}
	r, _, err := v.Round(4)
	if err != nil {
		t.Fatal(err)
	}
	if s := mustFixed(t, r, 4); s != "3.1356" {
		t.Fatalf("got %s, want 3.1356", s)
	}
	if s := mustFixed(t, decimal.MustParse("-7"), 2); s != "-7.00" {
		t.Fatalf("got %s, want -7.00", s)
	}
}
