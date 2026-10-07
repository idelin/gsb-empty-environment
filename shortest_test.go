package shortest

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func TestNonFiniteAndZero(t *testing.T) {
	cases := map[float64]string{
		math.NaN():           "NaN",
		math.Inf(1):          "+Inf",
		math.Inf(-1):         "-Inf",
		0:                    "0",
		math.Copysign(0, -1): "-0",
	}
	for f, want := range cases {
		if got := Format(f); got != want {
			t.Errorf("Format(%v) = %q, want %q", f, got, want)
		}
	}
}

func TestKnownValues(t *testing.T) {
	cases := []struct {
		f    float64
		want string
	}{
		{0.1, "0.1"},
		{0.2, "0.2"},
		{0.3, "0.3"},
		{1.0 / 3.0, "0.3333333333333333"},
		{2.0 / 3.0, "0.6666666666666666"},
		{math.Pi, "3.141592653589793"},
		{math.E, "2.718281828459045"},
		{1, "1"},
		{100000, "100000"},
		{1e6, "1e+06"},
		{123456789, "1.23456789e+08"},
		{0.0001, "0.0001"},
		{1e-5, "1e-05"},
		{math.MaxFloat64, "1.7976931348623157e+308"},
		{math.SmallestNonzeroFloat64, "5e-324"},
		{2.2250738585072014e-308, "2.2250738585072014e-308"}, // min normal
		{2.225073858507201e-308, "2.225073858507201e-308"},   // max subnormal
		{-7.038531e-26, "-7.038531e-26"},
		// Exact midpoints of two adjacent 16-digit decimals: ties resolve
		// toward the even last digit.
		{562949953421312.25, "5.629499534213122e+14"},
		{562949953421312.75, "5.629499534213128e+14"},
	}
	for _, c := range cases {
		if got := Format(c.f); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.f, got, c.want)
		}
	}
}

func roundTrips(t *testing.T, f float64) {
	t.Helper()
	s := Format(f)
	back, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("Format(%v) = %q does not parse: %v", f, s, err)
	}
	if math.Float64bits(back) != math.Float64bits(f) {
		t.Fatalf("Format(%v) = %q round-trips to %v", f, s, back)
	}
}

func TestRoundTripSpecials(t *testing.T) {
	vals := []float64{
		0, math.Copysign(0, -1), 1, -1, 0.5, 2, 10, 1e22, 1e23,
		math.MaxFloat64, -math.MaxFloat64,
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
		2.2250738585072014e-308, // min normal
		2.225073858507201e-308,  // max subnormal
		math.Pi, math.E, 1.0 / 3.0,
	}
	for _, f := range vals {
		roundTrips(t, f)
	}
}

// Adjacent floats must produce distinct strings, each round-tripping to its
// own value, across the whole exponent range including subnormals and powers
// of two (where the lower rounding gap is halved).
func TestAdjacentValues(t *testing.T) {
	bases := []float64{
		0, math.SmallestNonzeroFloat64,
		2.225073858507201e-308, 2.2250738585072014e-308, // subnormal/normal seam
		1, 0.1, math.Pi, 1e300, math.MaxFloat64,
	}
	for i := -1074; i <= 1023; i += 37 {
		bases = append(bases, math.Ldexp(1, i))
	}
	for _, b := range bases {
		for _, dir := range []float64{math.Inf(1), math.Inf(-1)} {
			f := b
			for i := 0; i < 3; i++ {
				next := math.Nextafter(f, dir)
				if f == next || math.IsInf(next, 0) {
					break
				}
				sf, sn := Format(f), Format(next)
				if sf == sn {
					t.Fatalf("adjacent values %v and %v share %q", f, next, sf)
				}
				roundTrips(t, f)
				roundTrips(t, next)
				f = next
			}
		}
	}
}

func TestSubnormals(t *testing.T) {
	for bits := uint64(1); bits <= 20000; bits++ {
		f := math.Float64frombits(bits)
		roundTrips(t, f)
		if got, want := Format(f), strconv.FormatFloat(f, 'g', -1, 64); got != want {
			t.Fatalf("Format(%v) = %q, strconv = %q", f, got, want)
		}
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50000; i++ {
		f := math.Float64frombits(rng.Uint64() & (1<<52 - 1)) // subnormal
		roundTrips(t, f)
		if got, want := Format(f), strconv.FormatFloat(f, 'g', -1, 64); got != want {
			t.Fatalf("Format(%v) = %q, strconv = %q", f, got, want)
		}
	}
}

// Every exponent combined with mantissas that stress the interval edges:
// powers of two (halved lower gap), neighbors of powers of two, and the
// largest/smallest mantissas.
func TestExponentSweep(t *testing.T) {
	mantissas := []uint64{0, 1, 2, 1 << 51, 1<<52 - 2, 1<<52 - 1, 0x1234567890abc}
	for bexp := 0; bexp < 0x7ff; bexp++ {
		for _, mant := range mantissas {
			if bexp == 0 && mant == 0 {
				continue
			}
			f := math.Float64frombits(uint64(bexp)<<52 | mant)
			roundTrips(t, f)
			if got, want := Format(f), strconv.FormatFloat(f, 'g', -1, 64); got != want {
				t.Fatalf("Format(%v) = %q, strconv = %q", f, got, want)
			}
		}
	}
}

// Differential cross-check against the standard library plus a round-trip
// assertion over random bit patterns, biased exponents and simple decimals.
func TestAgainstStdlibRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300000; i++ {
		var f float64
		switch i % 4 {
		case 0:
			f = math.Float64frombits(rng.Uint64())
		case 1: // every exponent equally likely
			bits := uint64(rng.Intn(0x7ff))<<52 | rng.Uint64()&(1<<52-1)
			f = math.Float64frombits(bits)
		case 2: // subnormals and tiny normals
			f = math.Float64frombits(rng.Uint64() % (1 << 53))
		case 3: // short decimals, exercising candidate selection
			f = float64(rng.Intn(2e6)-1e6) / math.Pow10(rng.Intn(8))
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		got := Format(f)
		if want := strconv.FormatFloat(f, 'g', -1, 64); got != want {
			t.Fatalf("Format(%v) = %q, strconv = %q", f, got, want)
		}
		back, err := strconv.ParseFloat(got, 64)
		if err != nil || math.Float64bits(back) != math.Float64bits(f) {
			t.Fatalf("Format(%v) = %q round-trips to %v (err=%v)", f, got, back, err)
		}
	}
}

// The shortest string must be minimal: dropping to fewer significant digits
// must never round-trip. Verified independently of strconv's formatter.
func TestMinimality(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 20000; i++ {
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
			continue
		}
		s := Format(f)
		nd := sigDigits(s)
		if nd <= 1 {
			continue
		}
		// Any (nd-1)-digit rounding of the value must not round-trip.
		back, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'g', nd-1, 64), 64)
		if math.Float64bits(back) == math.Float64bits(f) {
			t.Fatalf("Format(%v) = %q not minimal: %d digits suffice", f, s, nd-1)
		}
	}
}

// sigDigits counts significant decimal digits in the mantissa of s.
func sigDigits(s string) int {
	mant := s
	if i := strings.IndexByte(mant, 'e'); i >= 0 {
		mant = mant[:i]
	}
	hasDot := strings.IndexByte(mant, '.') >= 0
	mant = strings.TrimLeft(strings.NewReplacer("-", "", ".", "").Replace(mant), "0")
	if !hasDot {
		mant = strings.TrimRight(mant, "0")
	}
	return len(mant)
}

func BenchmarkFormat(b *testing.B) {
	rng := rand.New(rand.NewSource(9))
	vals := make([]float64, 64)
	for i := range vals {
		vals[i] = math.Float64frombits(rng.Uint64())
		for math.IsNaN(vals[i]) || math.IsInf(vals[i], 0) {
			vals[i] = math.Float64frombits(rng.Uint64())
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Format(vals[i%len(vals)])
	}
}
