package primeinterp

import "sort"

// Point is a single sample (x, y) in the prime field.
type Point struct {
	X, Y int64
}

// Interpolate returns the coefficients (constant term first) of the unique
// lowest-degree polynomial over GF(p) passing through every point.
//
// Duplicate x-coordinates are allowed as long as they carry the same y;
// a repeated x with a different y yields a *ConflictError. Trailing zero
// coefficients are trimmed, so the zero polynomial is reported as an
// empty slice. All inputs, including negative x and y, are reduced mod p.
func Interpolate(p int64, pts []Point) ([]int64, error) {
	if !isPrime(p) {
		return nil, ErrNotPrime
	}
	uniq, err := dedupe(p, pts)
	if err != nil {
		return nil, err
	}
	n := len(uniq)
	if n == 0 {
		return nil, nil
	}

	xs := make([]int64, n)
	// Newton divided differences.
	a := make([]int64, n)
	for i, pt := range uniq {
		xs[i] = pt.X
		a[i] = pt.Y
	}
	for j := 1; j < n; j++ {
		for i := n - 1; i >= j; i-- {
			num := mod(a[i]-a[i-1], p)
			den := mod(xs[i]-xs[i-j], p)
			a[i] = mulMod(num, invMod(den, p), p)
		}
	}

	// Convert Newton form to standard coefficients:
	// P(x) = a[0] + a[1](x-x0) + a[2](x-x0)(x-x1) + ...
	coeffs := []int64{a[n-1]}
	for k := n - 2; k >= 0; k-- {
		next := make([]int64, len(coeffs)+1)
		for i, c := range coeffs {
			// multiply by (x - xs[k])
			next[i+1] = mod(next[i+1]+c, p)
			next[i] = mod(next[i]-mulMod(c, xs[k], p), p)
		}
		next[0] = mod(next[0]+a[k], p)
		coeffs = next
	}
	return trim(coeffs), nil
}

// Evaluate computes the polynomial with the given coefficients
// (constant term first) at x over GF(p), using Horner's rule.
// Negative inputs are reduced mod p.
func Evaluate(p int64, coeffs []int64, x int64) int64 {
	acc := int64(0)
	xm := mod(x, p)
	for i := len(coeffs) - 1; i >= 0; i-- {
		acc = mod(mulMod(acc, xm, p)+mod(coeffs[i], p), p)
	}
	return acc
}

// dedupe reduces coordinates mod p, sorts by x and merges duplicates,
// rejecting repeated x values that disagree on y.
func dedupe(p int64, pts []Point) ([]Point, error) {
	reduced := make([]Point, len(pts))
	for i, pt := range pts {
		reduced[i] = Point{X: mod(pt.X, p), Y: mod(pt.Y, p)}
	}
	sort.Slice(reduced, func(i, j int) bool { return reduced[i].X < reduced[j].X })
	uniq := reduced[:0]
	for _, pt := range reduced {
		if len(uniq) > 0 && uniq[len(uniq)-1].X == pt.X {
			if uniq[len(uniq)-1].Y != pt.Y {
				return nil, &ConflictError{X: pt.X, Y1: uniq[len(uniq)-1].Y, Y2: pt.Y}
			}
			continue
		}
		uniq = append(uniq, pt)
	}
	return uniq, nil
}

// trim drops trailing zero coefficients so the result is canonical.
func trim(coeffs []int64) []int64 {
	for len(coeffs) > 0 && coeffs[len(coeffs)-1] == 0 {
		coeffs = coeffs[:len(coeffs)-1]
	}
	return coeffs
}
