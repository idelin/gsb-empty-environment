package decimal128

import (
	"errors"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// TestExtremeRandom uses full 38-digit operands so every intermediate value
// exercises the 256-bit wide path and the scale-reduction rules.
func TestExtremeRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	limit := new(big.Int).Sub(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil),
		big.NewInt(1),
	)
	for iter := 0; iter < 200; iter++ {
		for _, mode := range []RoundingMode{HalfAwayFromZero, HalfToEven} {
			la, ra := randomExtreme(rng)
			lb, rb := randomExtreme(rng)
			a := mustParse(t, la, mode)
			b := mustParse(t, lb, mode)

			prod := new(big.Rat).Mul(ra, rb)
			mulScale := a.Scale() + b.Scale()
			if mulScale > 38 {
				mulScale = 38
			}
			if got, ok := try2(MulMode(a, b, mode)); ok {
				checkRat(t, mode, "MulX", la, lb, got, prod, mulScale, limit)
			} else if !overflowsRat(prod, mulScale, limit) {
				t.Errorf("Mul(%q,%q) unexpected overflow", la, lb)
			}

			if rb.Sign() != 0 {
				quot := new(big.Rat).Quo(ra, rb)
				divScale := 6
				if cand := a.Scale() + b.Precision() + 1; cand > divScale {
					divScale = cand
				}
				if divScale > 38 {
					divScale = 38
				}
				if got, ok := try2(DivMode(a, b, mode)); ok {
					checkRat(t, mode, "DivX", la, lb, got, quot, divScale, limit)
				} else if !overflowsRat(quot, divScale, limit) {
					t.Errorf("Div(%q,%q) unexpected overflow", la, lb)
				}
			}

			aligned := a.Scale()
			if b.Scale() > aligned {
				aligned = b.Scale()
			}
			if got, ok := try2(AddMode(a, b, mode)); ok {
				checkRat(t, mode, "AddX", la, lb, got, new(big.Rat).Add(ra, rb), aligned, limit)
			}
		}
	}
}

func randomExtreme(rng *rand.Rand) (string, *big.Rat) {
	n := 38
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i == 0 {
			b.WriteByte(byte('1' + rng.Intn(9)))
		} else {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	digits := b.String()
	scale := rng.Intn(39)
	literal := digits
	if scale > 0 {
		literal = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if rng.Intn(2) == 0 {
		literal = "-" + literal
	}
	return literal, parseRat(literal)
}

// TestMultiSeedStress reruns the full arithmetic comparison under several
// independent RNG seeds, covering roughly 2400 random operand pairs in total.
func TestMultiSeedStress(t *testing.T) {
	limit := new(big.Int).Sub(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil),
		big.NewInt(1),
	)
	for _, seed := range []int64{1, 7, 99, 20261127, 1 << 40} {
		rng := rand.New(rand.NewSource(seed))
		for iter := 0; iter < 150; iter++ {
			la, ra := randomDecimal(rng)
			lb, rb := randomDecimal(rng)
			a := mustParse(t, la, HalfToEven)
			b := mustParse(t, lb, HalfToEven)
			aligned := a.Scale()
			if b.Scale() > aligned {
				aligned = b.Scale()
			}
			if got, ok := try2(AddMode(a, b, HalfToEven)); ok {
				checkRat(t, HalfToEven, "stress-add", la, lb, got,
					new(big.Rat).Add(ra, rb), aligned, limit)
			}
			if got, ok := try2(SubMode(a, b, HalfAwayFromZero)); ok {
				checkRat(t, HalfAwayFromZero, "stress-sub", la, lb, got,
					new(big.Rat).Sub(ra, rb), aligned, limit)
			}
			mulScale := a.Scale() + b.Scale()
			if mulScale > 38 {
				mulScale = 38
			}
			if got, ok := try2(MulMode(a, b, HalfToEven)); ok {
				checkRat(t, HalfToEven, "stress-mul", la, lb, got,
					new(big.Rat).Mul(ra, rb), mulScale, limit)
			}
			if rb.Sign() != 0 {
				divScale := 6
				if cand := a.Scale() + b.Precision() + 1; cand > divScale {
					divScale = cand
				}
				if divScale > 38 {
					divScale = 38
				}
				if got, ok := try2(DivMode(a, b, HalfToEven)); ok {
					checkRat(t, HalfToEven, "stress-div", la, lb, got,
						new(big.Rat).Quo(ra, rb), divScale, limit)
				}
			}
		}
	}
}

// TestScientificRoundTrip parses scientific literals and compares the exact
// rational value against the fixed-point rendering.
func TestScientificRoundTrip(t *testing.T) {
	cases := []string{
		"1.2345678901234567890123456789012345678E+5",
		"9.8765432109876543210987654321098765432E-10",
		"-1.0000000000000000000000000000000000001E+20",
		"5E0",
		"5e-3",
		"6.02214076e23",
	}
	for _, s := range cases {
		d, err := Parse(s, HalfAwayFromZero)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		rat := parseRat(s)
		if got := d.String(); got != ratFixed(rat, d.Scale()) {
			t.Errorf("Parse(%q) = %s, want %s", s, got, ratFixed(rat, d.Scale()))
		}
	}
}

// TestTrailingZerosAndNegZero checks scale-driven padding and sign cleanup.
func TestTrailingZerosAndNegZero(t *testing.T) {
	d := mustParse(t, "1.5", HalfAwayFromZero)
	if d.String() != "1.5" {
		t.Errorf("got %s", d.String())
	}
	z := mustParse(t, "-0.0000", HalfAwayFromZero)
	if z.Sign() != 0 || z.String() != "0.0000" {
		t.Errorf("negative zero normalization failed: %s sign=%d", z.String(), z.Sign())
	}
	if (Decimal128{}).String() != "0" {
		t.Errorf("zero value renders as %q", (Decimal128{}).String())
	}
}

// TestParseOverflowGuards covers literals whose magnitude cannot fit even the
// intermediate 256-bit coefficient or whose integer part exceeds 38 digits.
func TestParseOverflowGuards(t *testing.T) {
	cases := []string{
		strings.Repeat("9", 78),
		"1e100",
		"0." + strings.Repeat("9", 78),
		strings.Repeat("9", 39), // 39 integer digits
	}
	for _, s := range cases {
		if _, err := Parse(s, HalfAwayFromZero); !errors.Is(err, ErrOverflow) {
			t.Errorf("Parse(%q) err = %v, want ErrOverflow", s, err)
		}
	}
	// A 39-digit fraction that rounds back to 38 digits is accepted.
	longFrac := "0." + strings.Repeat("9", 38) + "4"
	d, err := Parse(longFrac, HalfAwayFromZero)
	if err != nil {
		t.Errorf("long fraction rejected: %v", err)
	}
	if d.Scale() != 38 {
		t.Errorf("long fraction scale = %d, want 38", d.Scale())
	}
}

// TestNewConstructor covers the int64 constructor and its validation.
func TestNewConstructor(t *testing.T) {
	d, err := New(-12345, 2)
	if err != nil || d.String() != "-123.45" {
		t.Fatalf("New(-12345,2) = %s, %v", d.String(), err)
	}
	if d.Precision() != 5 || d.Scale() != 2 {
		t.Errorf("metadata p=%d s=%d, want 5,2", d.Precision(), d.Scale())
	}
	z, err := New(0, 38)
	if err != nil || z.String() != "0.00000000000000000000000000000000000000" {
		t.Errorf("zero at scale 38 = %s, %v", z.String(), err)
	}
	if _, err := New(1, 39); !errors.Is(err, ErrInvalidScale) {
		t.Errorf("scale 39 err = %v", err)
	}
}

// TestCompare checks exact ordering across different scales and signs.
func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"-0.5", "0.25", -1},
		{"0.25", "-0.5", 1},
		{"0.5", "0.500", 0},
		{"1.000", "0.9999", 1},
		{"-1.000", "-0.9999", -1},
		{"0", "-0.00", 0},
		{"10000000000000000000000000000000000000", "9999999999999999999999999999999999999", 1},
	}
	for _, tc := range cases {
		a := mustParse(t, tc.a, HalfAwayFromZero)
		b := mustParse(t, tc.b, HalfAwayFromZero)
		got, err := Cmp(a, b)
		if err != nil || got != tc.want {
			t.Errorf("Cmp(%s,%s) = %d, %v; want %d", tc.a, tc.b, got, err, tc.want)
		}
	}
}
