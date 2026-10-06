// Package interp provides polynomial interpolation over a prime field and a
// revisioned sample stream that tracks the current interpolating polynomial.
package interp

// Field is the prime field Z/pZ. All arithmetic normalizes results into
// [0, p), including for negative inputs.
type Field struct {
	p int64
}

// NewField returns the prime field Z/pZ. p must be a positive prime; the
// primality of p is the caller's responsibility.
func NewField(p int64) Field {
	if p <= 1 {
		panic("interp: modulus must be greater than 1")
	}
	return Field{p: p}
}

// Modulus returns the prime modulus p.
func (f Field) Modulus() int64 { return f.p }

// Norm reduces a into [0, p), handling negative inputs.
func (f Field) Norm(a int64) int64 {
	r := a % f.p
	if r < 0 {
		r += f.p
	}
	return r
}

// Add returns (a + b) mod p.
func (f Field) Add(a, b int64) int64 { return f.Norm(f.Norm(a) + f.Norm(b)) }

// Sub returns (a - b) mod p.
func (f Field) Sub(a, b int64) int64 { return f.Norm(f.Norm(a) - f.Norm(b)) }

// Mul returns (a * b) mod p.
func (f Field) Mul(a, b int64) int64 { return f.Norm(f.Norm(a) * f.Norm(b)) }

// Neg returns -a mod p.
func (f Field) Neg(a int64) int64 { return f.Norm(-f.Norm(a)) }

// Pow returns a^e mod p for e >= 0.
func (f Field) Pow(a, e int64) int64 {
	if e < 0 {
		panic("interp: negative exponent")
	}
	base := f.Norm(a)
	result := int64(1)
	for e > 0 {
		if e&1 == 1 {
			result = f.Mul(result, base)
		}
		base = f.Mul(base, base)
		e >>= 1
	}
	return result
}

// Inv returns the multiplicative inverse of a mod p. It panics if a == 0
// (mod p). Uses Fermat's little theorem, valid because p is prime.
func (f Field) Inv(a int64) int64 {
	a = f.Norm(a)
	if a == 0 {
		panic("interp: inverse of zero")
	}
	return f.Pow(a, f.p-2)
}

// Div returns (a / b) mod p. It panics if b == 0 (mod p).
func (f Field) Div(a, b int64) int64 { return f.Mul(a, f.Inv(b)) }

// Eval evaluates the polynomial with the given coefficients (coeffs[i] is the
// coefficient of x^i) at x, all mod p. Negative x and coefficients are
// handled. A nil or empty coefficient slice is the zero polynomial.
func (f Field) Eval(coeffs []int64, x int64) int64 {
	x = f.Norm(x)
	acc := int64(0)
	for i := len(coeffs) - 1; i >= 0; i-- {
		acc = f.Add(f.Mul(acc, x), coeffs[i])
	}
	return acc
}
