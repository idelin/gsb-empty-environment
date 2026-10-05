package slideq

// Fraction is a normalized rational number Num/Den with Den > 0.
type Fraction struct {
	Num int64
	Den int64
}

// NewFraction returns num/den in lowest terms. It panics if den == 0.
func NewFraction(num, den int64) Fraction {
	if den == 0 {
		panic("slideq: zero denominator")
	}
	if den < 0 {
		num, den = -num, -den
	}
	g := gcd(abs64(num), den)
	return Fraction{Num: num / g, Den: den / g}
}

// MulFloor returns floor(f * v) for v >= 0, assuming f >= 0.
func (f Fraction) MulFloor(v int64) int64 {
	return f.Num * v / f.Den
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
