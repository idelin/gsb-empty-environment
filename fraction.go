package sliding

import "errors"

// Fraction is a rational number in [0, 1], kept in lowest terms.
type Fraction struct {
	Num int64
	Den int64
}

// NewFraction builds num/den, requiring 0 <= num <= den and den > 0.
func NewFraction(num, den int64) (Fraction, error) {
	if den <= 0 {
		return Fraction{}, errors.New("sliding: denominator must be positive")
	}
	if num < 0 || num > den {
		return Fraction{}, errors.New("sliding: q must be within [0, 1]")
	}
	g := gcd(num, den)
	if g == 0 {
		g = 1
	}
	return Fraction{Num: num / g, Den: den / g}, nil
}

// MustFraction is NewFraction that panics on invalid input.
func MustFraction(num, den int64) Fraction {
	f, err := NewFraction(num, den)
	if err != nil {
		panic(err)
	}
	return f
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
