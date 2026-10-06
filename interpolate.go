package interp

import "errors"

// ErrConflict is returned when two samples share the same x-coordinate but
// carry different y values.
var ErrConflict = errors.New("interp: conflicting samples for the same x")

// ErrNoSamples is returned when interpolation is requested with no samples.
var ErrNoSamples = errors.New("interp: no samples")

// Interpolate returns the coefficients of the unique lowest-degree polynomial
// over f passing through all samples (xs[i], ys[i]). coeffs[i] is the
// coefficient of x^i and the result is normalized by trimming trailing zero
// coefficients, so the zero polynomial is represented by an empty slice.
//
// Redundant samples (same x, same y, after normalization) are tolerated and
// collapsed. Samples with the same x but different y are rejected with
// ErrConflict. Coordinates may be negative or >= p; they are reduced mod p
// before use.
func Interpolate(f Field, xs, ys []int64) ([]int64, error) {
	if len(xs) != len(ys) {
		panic("interp: xs and ys length mismatch")
	}
	// Deduplicate by normalized x, rejecting contradictory y.
	ux := make([]int64, 0, len(xs))
	uy := make([]int64, 0, len(xs))
	seen := make(map[int64]int64, len(xs))
	for i := range xs {
		x := f.Norm(xs[i])
		y := f.Norm(ys[i])
		if prev, ok := seen[x]; ok {
			if prev != y {
				return nil, ErrConflict
			}
			continue
		}
		seen[x] = y
		ux = append(ux, x)
		uy = append(uy, y)
	}
	if len(ux) == 0 {
		return nil, ErrNoSamples
	}

	// Lagrange interpolation accumulated in the coefficient basis.
	coeffs := make([]int64, len(ux))
	for i := range ux {
		// basis polynomial: product over j != i of (x - ux[j]) / (ux[i] - ux[j])
		basis := []int64{1}
		denom := int64(1)
		for j := range ux {
			if j == i {
				continue
			}
			basis = mulByLinear(f, basis, f.Neg(ux[j]))
			denom = f.Mul(denom, f.Sub(ux[i], ux[j]))
		}
		scale := f.Div(uy[i], denom)
		for k, c := range basis {
			coeffs[k] = f.Add(coeffs[k], f.Mul(scale, c))
		}
	}
	return Trim(f, coeffs), nil
}

// mulByLinear multiplies poly (coefficient basis) by (x + c) over f.
func mulByLinear(f Field, poly []int64, c int64) []int64 {
	out := make([]int64, len(poly)+1)
	for i, a := range poly {
		out[i] = f.Add(out[i], f.Mul(a, c))
		out[i+1] = f.Add(out[i+1], a)
	}
	return out
}

// Trim removes trailing zero coefficients (mod p) so the result is the
// canonical lowest-degree representation.
func Trim(f Field, coeffs []int64) []int64 {
	n := len(coeffs)
	for n > 0 && f.Norm(coeffs[n-1]) == 0 {
		n--
	}
	out := make([]int64, n)
	for i := range out {
		out[i] = f.Norm(coeffs[i])
	}
	return out
}
