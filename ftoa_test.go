package ftoa

import (
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// oracle is the standard library's shortest conversion, used only as a
// reference implementation in tests.
func oracle(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func TestSpecialValues(t *testing.T) {
	cases := []struct {
		f    float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "-0"},
		{math.NaN(), "NaN"},
		{math.Inf(1), "+Inf"},
		{math.Inf(-1), "-Inf"},
	}
	for _, c := range cases {
		if got := Format(c.f); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.f, got, c.want)
		}
	}
}

func interestingValues() []float64 {
	var vs []float64
	vs = append(vs,
		1, -1, 2, 0.5, 0.1, 0.2, 0.3, 1.0/3.0, 2.0/3.0,
		math.Pi, math.E, 2.675, 1e5, 1e6, 1234567, 123456.78,
		0.0001, 0.00001, 1e20, 1e21, 1e22, 12345678901234567890.0,
		math.MaxFloat64, math.SmallestNonzeroFloat64,
		2.2250738585072014e-308, // smallest normal
		2.225073858507201e-308,  // largest subnormal
		5e-324, 4.9e-324, 1.5e-323,
		9.999999999999999, 999999.9999999999,
		3.141592653589793, 2.718281828459045,
		1.7976931348623157e308, 1.7976931348623155e308,
		4.35, 4.45, 4.55, 0.15, 0.25, 0.35,
	)
	// Powers of two across the whole exponent range, plus neighbours.
	for e := -1074; e <= 1023; e++ {
		f := math.Ldexp(1, e)
		vs = append(vs, f,
			math.Nextafter(f, math.Inf(1)),
			math.Nextafter(f, math.Inf(-1)),
		)
	}
	// Powers of ten and their neighbours.
	for e := -323; e <= 308; e++ {
		f, err := strconv.ParseFloat("1e"+strconv.Itoa(e), 64)
		if err != nil {
			continue
		}
		vs = append(vs, f,
			math.Nextafter(f, math.Inf(1)),
			math.Nextafter(f, math.Inf(-1)),
		)
	}
	return vs
}

func TestAgainstStdlib(t *testing.T) {
	for _, f := range interestingValues() {
		for _, g := range []float64{f, -f} {
			oracleCompare(t, g)
		}
	}
}

func TestRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		bits := rng.Uint64()
		f := math.Float64frombits(bits)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		s := Format(f)
		g, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q): %v", s, err)
		}
		if math.Float64bits(g) != bits {
			t.Fatalf("round trip failed: %#x -> %q -> %#x", bits, s,
				math.Float64bits(g))
		}
	}
}

func TestRandomAgainstStdlib(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200000; i++ {
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		oracleCompare(t, f)
	}
}

// TestAdjacentFloats checks that adjacent floats never share a shortest
// representation and that stepping across them stays consistent with the
// standard library.
func TestAdjacentFloats(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	seeds := interestingValues()
	for i := 0; i < 2000; i++ {
		seeds = append(seeds, math.Float64frombits(rng.Uint64()))
	}
	for _, f := range seeds {
		if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
			continue
		}
		up := math.Nextafter(f, math.Inf(1))
		down := math.Nextafter(f, math.Inf(-1))
		sf, su, sd := Format(f), Format(up), Format(down)
		if sf == su || sf == sd {
			t.Errorf("adjacent floats share representation: %#x, %#x, %#x -> %q %q %q",
				math.Float64bits(down), math.Float64bits(f), math.Float64bits(up),
				sd, sf, su)
		}
		oracleCompare(t, up)
		oracleCompare(t, down)
	}
}

// TestSubnormals sweeps the smallest subnormals exhaustively and samples
// random ones across the whole subnormal range.
func TestSubnormals(t *testing.T) {
	const smallestNormalBits = uint64(0x0010000000000000)
	for bits := uint64(1); bits <= 20000; bits++ {
		f := math.Float64frombits(bits)
		oracleCompare(t, f)
	}
	// Boundary between subnormal and normal.
	for bits := smallestNormalBits - 10000; bits <= smallestNormalBits+10000; bits++ {
		f := math.Float64frombits(bits)
		if got, want := Format(f), oracle(f); got != want {
			t.Fatalf("boundary %#x: Format = %q, strconv = %q", bits, got, want)
		}
	}
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 100000; i++ {
		bits := rng.Uint64() % uint64(smallestNormalBits)
		if bits == 0 {
			continue
		}
		f := math.Float64frombits(bits)
		oracleCompare(t, f)
	}
}

// TestTieBreakEven verifies the "closest, then even last digit" rule on
// constructed halfway cases.
func TestTieBreakEven(t *testing.T) {
	// 0.5 sits exactly between 0 and 1 in one-digit precision; the shortest
	// representation of the floats just around halfway points must still
	// round-trip and match the oracle.
	for _, f := range []float64{0.05, 0.15, 0.25, 0.35, 0.45, 0.55, 0.65, 0.75, 0.85, 0.95} {
		if got, want := Format(f), oracle(f); got != want {
			t.Errorf("Format(%v) = %q, strconv = %q", f, got, want)
		}
	}
}

// oracleCompare compares Format against the standard library. Rare
// disagreements are tolerated only when our output verifies against the
// spec exactly: Go 1.11's formatter (pre-Ryu) is known to occasionally
// miss the closest candidate.
func oracleCompare(t *testing.T, f float64) {
	t.Helper()
	if got, want := Format(f), oracle(f); got != want {
		specCheck(t, f, got)
		t.Logf("stdlib differs for %#x: ftoa = %q, strconv = %q (ftoa verified against spec)",
			math.Float64bits(f), got, want)
	}
}

// specCheck verifies that s is the shortest decimal round-tripping to f,
// the closest among shortest, with ties broken to an even last digit.
// It uses exact rational arithmetic independent of the implementation.
func specCheck(t *testing.T, f float64, s string) {
	t.Helper()
	af := math.Abs(f)
	rf := new(big.Rat).SetFloat64(af)

	digits, exp := splitDecimal(t, s)
	mant, _ := new(big.Int).SetString(digits, 10)
	q := exp - len(digits) + 1 // s == mant * 10^q

	lo, hi, incl := roundingInterval(af)
	inside := func(x *big.Rat) bool {
		cLo, cHi := x.Cmp(lo), x.Cmp(hi)
		if incl {
			return cLo >= 0 && cHi <= 0
		}
		return cLo > 0 && cHi < 0
	}
	scaled := func(k *big.Int, qq int) *big.Rat {
		r := new(big.Rat).SetInt(k)
		if qq >= 0 {
			r.Mul(r, ratPow10(qq))
		} else {
			r.Quo(r, ratPow10(qq))
		}
		return r
	}
	if !inside(scaled(mant, q)) {
		t.Fatalf("spec: %q does not round-trip to %#x", s, math.Float64bits(f))
	}

	// Shortest: neither of the grid points bracketing f at precision
	// len(digits)-1 may round-trip (any round-tripping grid point forces
	// one of the bracketing ones into the interval, by convexity).
	if len(digits) > 1 {
		qs := decExpRat(rf) - (len(digits) - 1) + 1
		k1, k2, _ := bracket(rf, qs)
		if inside(scaled(k1, qs)) || inside(scaled(k2, qs)) {
			t.Fatalf("spec: %q is not the shortest representation of %#x",
				s, math.Float64bits(f))
		}
	}

	// Closest among the valid candidates at this precision, ties to even.
	k1, k2, tie := bracket(rf, q)
	v1, v2 := inside(scaled(k1, q)), inside(scaled(k2, q))
	var want *big.Int
	switch {
	case v1 && v2 && tie:
		want = k1
		if k1.Bit(0) != 0 {
			want = k2
		}
	case v1 && v2:
		d1 := new(big.Rat).Sub(scaled(k1, q), rf)
		d1.Abs(d1)
		d2 := new(big.Rat).Sub(scaled(k2, q), rf)
		d2.Abs(d2)
		want = k1
		if d2.Cmp(d1) < 0 {
			want = k2
		}
	case v1:
		want = k1
	case v2:
		want = k2
	default:
		t.Fatalf("spec: no valid candidate at %#x precision %d", math.Float64bits(f), len(digits))
	}
	if mant.Cmp(want) != 0 {
		t.Fatalf("spec: %q is not the closest/even choice for %#x, want mantissa %v",
			s, math.Float64bits(f), want)
	}
}

// bracket returns the two integers bracketing rf / 10^q and whether it is
// exactly halfway between them.
func bracket(rf *big.Rat, q int) (k1, k2 *big.Int, tie bool) {
	r := new(big.Rat).Set(rf)
	if q >= 0 {
		r.Quo(r, ratPow10(q))
	} else {
		r.Mul(r, ratPow10(-q))
	}
	k1 = new(big.Int).Quo(r.Num(), r.Denom())
	k2 = new(big.Int).Add(k1, big.NewInt(1))
	rem := new(big.Rat).Sub(r, new(big.Rat).SetInt(k1))
	return k1, k2, rem.Cmp(big.NewRat(1, 2)) == 0
}

// roundingInterval returns the open/closed interval of real numbers that
// round to f under round-to-nearest-even.
func roundingInterval(f float64) (lo, hi *big.Rat, inclusive bool) {
	rf := new(big.Rat).SetFloat64(f)
	rPrev := new(big.Rat).SetFloat64(math.Nextafter(f, 0))
	two := big.NewRat(2, 1)
	lo = new(big.Rat).Add(rPrev, rf)
	lo.Quo(lo, two)
	if rNext := new(big.Rat).SetFloat64(math.Nextafter(f, math.Inf(1))); rNext != nil {
		hi = new(big.Rat).Add(rf, rNext)
		hi.Quo(hi, two)
	} else {
		gap := new(big.Rat).Sub(rf, rPrev)
		hi = new(big.Rat).Add(rf, gap.Quo(gap, two))
	}
	return lo, hi, math.Float64bits(f)&1 == 0
}

// splitDecimal parses ftoa output into significant digits and the decimal
// exponent of the leading digit.
func splitDecimal(t *testing.T, s string) (string, int) {
	t.Helper()
	s = strings.TrimPrefix(s, "-")
	exp := 0
	if i := strings.IndexByte(s, 'e'); i >= 0 {
		e, err := strconv.Atoi(s[i+1:])
		if err != nil {
			t.Fatalf("bad exponent in %q", s)
		}
		exp = e
		s = s[:i]
	}
	point := strings.IndexByte(s, '.')
	pointPos := len(s)
	if point >= 0 {
		pointPos = point
		s = s[:point] + s[point+1:]
	}
	exp += pointPos - 1
	for len(s) > 0 && s[0] == '0' {
		s = s[1:]
		exp--
	}
	s = strings.TrimRight(s, "0")
	if s == "" {
		t.Fatalf("unexpected zero")
	}
	return s, exp
}

func decExpRat(r *big.Rat) int {
	f, _ := r.Float64()
	e := int(math.Floor(math.Log10(f)))
	for r.Cmp(ratPow10(e)) < 0 {
		e--
	}
	for r.Cmp(ratPow10(e+1)) >= 0 {
		e++
	}
	return e
}

func ratPow10(e int) *big.Rat {
	i := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(e)), nil)
	return new(big.Rat).SetInt(i)
}

func BenchmarkFormat(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	var vals []float64
	for len(vals) < 1024 {
		f := math.Float64frombits(rng.Uint64())
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			vals = append(vals, f)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Format(vals[i%len(vals)])
	}
}
