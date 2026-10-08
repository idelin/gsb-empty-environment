package tileindex

// Rat is an exact rational number Num/Den with Den > 0, kept in lowest
// terms. Texture intervals are exact rationals so boundary tiles line up
// with the coordinate scaling formula without floating-point error.
type Rat struct {
	Num, Den int64
}

// NewRat returns n/d normalised to lowest terms with a positive
// denominator. d must be non-zero.
func NewRat(n, d int64) Rat {
	if d == 0 {
		panic("tileindex: zero denominator")
	}
	if d < 0 {
		n, d = -n, -d
	}
	g := gcd(abs(n), d)
	return Rat{Num: n / g, Den: d / g}
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// Float64 returns the nearest float64 approximation, for rendering only.
func (r Rat) Float64() float64 { return float64(r.Num) / float64(r.Den) }

// Rect is an axis-aligned texture interval [U0,U1]x[V0,V1] inside a tile,
// with exact rational edges. The full tile is [0,1]x[0,1].
type Rect struct {
	U0, V0, U1, V1 Rat
}
