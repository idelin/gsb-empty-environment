package algebraic

import "math/big"

// Polynomials over F_p, used to certify irreducibility over Q:
// if a primitive integer polynomial is irreducible modulo some prime
// (with the same degree), it is irreducible over Q.

type fpoly []int64 // coefficients mod p, normalized: no trailing zeros

func fnorm(f fpoly, p int64) fpoly {
	n := len(f)
	for n > 0 && f[n-1]%p == 0 {
		n--
	}
	return f[:n]
}

func fadd(f, g fpoly, p int64) fpoly {
	n := len(f)
	if len(g) > n {
		n = len(g)
	}
	r := make(fpoly, n)
	for i := range r {
		var a, b int64
		if i < len(f) {
			a = f[i]
		}
		if i < len(g) {
			b = g[i]
		}
		r[i] = (a + b) % p
	}
	return fnorm(r, p)
}

func fsub(f, g fpoly, p int64) fpoly {
	n := len(f)
	if len(g) > n {
		n = len(g)
	}
	r := make(fpoly, n)
	for i := range r {
		var a, b int64
		if i < len(f) {
			a = f[i]
		}
		if i < len(g) {
			b = g[i]
		}
		r[i] = (((a - b) % p) + p) % p
	}
	return fnorm(r, p)
}

func fmul(f, g fpoly, p int64) fpoly {
	if len(f) == 0 || len(g) == 0 {
		return nil
	}
	r := make(fpoly, len(f)+len(g)-1)
	for i, a := range f {
		for j, b := range g {
			r[i+j] = (r[i+j] + a*b) % p
		}
	}
	return fnorm(r, p)
}

func frem(f, g fpoly, p int64) fpoly {
	r := make(fpoly, len(f))
	copy(r, f)
	r = fnorm(r, p)
	invLead := modInv(g[len(g)-1], p)
	for len(r) >= len(g) && len(r) > 0 {
		s := len(r) - len(g)
		coeff := r[len(r)-1] * invLead % p
		for j := 0; j < len(g); j++ {
			r[s+j] = (((r[s+j] - coeff*g[j]) % p) + p) % p
		}
		r = fnorm(r, p)
	}
	return r
}

func fgcd(f, g fpoly, p int64) fpoly {
	for len(g) > 0 {
		f, g = g, frem(f, g, p)
	}
	if len(f) == 0 {
		return nil
	}
	inv := modInv(f[len(f)-1], p)
	for i := range f {
		f[i] = f[i] * inv % p
	}
	return f
}

func fderiv(f fpoly, p int64) fpoly {
	if len(f) <= 1 {
		return nil
	}
	r := make(fpoly, len(f)-1)
	for i := 1; i < len(f); i++ {
		r[i-1] = f[i] * (int64(i) % p) % p
	}
	return fnorm(r, p)
}

func fpowmod(base fpoly, e int64, mod fpoly, p int64) fpoly {
	r := fpoly{1}
	b := frem(base, mod, p)
	for e > 0 {
		if e&1 == 1 {
			r = frem(fmul(r, b, p), mod, p)
		}
		b = frem(fmul(b, b, p), mod, p)
		e >>= 1
	}
	return r
}

func fisOne(f fpoly) bool { return len(f) == 1 && f[0] == 1 }

func modPow(a, e, p int64) int64 {
	r := int64(1)
	a = ((a % p) + p) % p
	for e > 0 {
		if e&1 == 1 {
			r = r * a % p
		}
		a = a * a % p
		e >>= 1
	}
	return r
}

func modInv(a, p int64) int64 { return modPow(a, p-2, p) }

func primeDivisors(n int) []int {
	var res []int
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			res = append(res, d)
			for n%d == 0 {
				n /= d
			}
		}
	}
	if n > 1 {
		res = append(res, n)
	}
	return res
}

// irreducibleModP reports whether f (a primitive integer polynomial)
// is certified irreducible modulo the prime p via Rabin's test.
// It returns false when p divides the leading coefficient, when f is
// not square-free mod p, or when f is reducible mod p.
func irreducibleModP(f []*big.Int, p int64) bool {
	bp := big.NewInt(p)
	cf := make(fpoly, len(f))
	for i, k := range f {
		cf[i] = new(big.Int).Mod(k, bp).Int64()
	}
	cf = fnorm(cf, p)
	n := len(cf) - 1
	if n != len(f)-1 || n < 1 {
		return false
	}
	if n == 1 {
		return true
	}
	inv := modInv(cf[n], p)
	for i := range cf {
		cf[i] = cf[i] * inv % p
	}
	// Square-free check; otherwise we cannot certify.
	if !fisOne(fgcd(cf, fderiv(cf, p), p)) {
		return false
	}
	// Rabin's irreducibility test.
	x := fpoly{0, 1}
	h := x
	for i := 0; i < n; i++ {
		h = fpowmod(h, p, cf, p)
	}
	if len(fsub(h, x, p)) != 0 {
		return false
	}
	for _, q := range primeDivisors(n) {
		g := x
		for i := 0; i < n/q; i++ {
			g = fpowmod(g, p, cf, p)
		}
		if !fisOne(fgcd(cf, fsub(g, x, p), p)) {
			return false
		}
	}
	return true
}

// irreducibleOverQ tries to certify irreducibility of the primitive
// integer polynomial f by reduction modulo small primes.
func irreducibleOverQ(f []*big.Int) bool {
	primes := []int64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71}
	for _, p := range primes {
		if irreducibleModP(f, p) {
			return true
		}
	}
	return false
}
