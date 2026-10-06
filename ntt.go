package convlib

// NTT-friendly primes of the form p = c*2^k + 1. Each is < 2^30, so the
// product of two reduced residues fits in a uint64 (< 2^60): no overflow.
type nttPrime struct {
	p      uint64 // prime modulus
	g      uint64 // primitive root modulo p
	maxLog uint   // largest L such that 2^L divides p-1 (max NTT length 2^L)
}

// nttPrimes is the ordered table of moduli used by the CRT reconstruction.
// Primes are consumed from the front until their product exceeds 2*B, where
// B is the proven coefficient bound.
var nttPrimes = []nttPrime{
	{998244353, 3, 23},  // 119 * 2^23 + 1
	{1004535809, 3, 21}, // 479 * 2^21 + 1
	{469762049, 3, 26},  // 7 * 2^26 + 1
	{167772161, 3, 25},  // 5 * 2^25 + 1
	{754974721, 11, 24}, // 45 * 2^24 + 1
}

func powmod(base, exp, mod uint64) uint64 {
	result := uint64(1)
	base %= mod
	for exp > 0 {
		if exp&1 != 0 {
			result = result * base % mod
		}
		base = base * base % mod
		exp >>= 1
	}
	return result
}

// ntt computes the in-place number theoretic transform of a modulo p.
// len(a) must be a power of two not exceeding 2^maxLog of the prime.
func ntt(a []uint64, invert bool, p, g uint64) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		wlen := powmod(g, (p-1)/uint64(length), p)
		if invert {
			wlen = powmod(wlen, p-2, p)
		}
		half := length >> 1
		for i := 0; i < n; i += length {
			w := uint64(1)
			for j := 0; j < half; j++ {
				u := a[i+j]
				v := a[i+j+half] * w % p
				s := u + v
				if s >= p {
					s -= p
				}
				a[i+j] = s
				d := u + p - v
				if d >= p {
					d -= p
				}
				a[i+j+half] = d
				w = w * wlen % p
			}
		}
	}
	if invert {
		ninv := powmod(uint64(n), p-2, p)
		for i := range a {
			a[i] = a[i] * ninv % p
		}
	}
}

// convMod returns the first need coefficients of the cyclic-safe (zero-padded)
// convolution of a and b reduced modulo the given prime.
func convMod(a, b []uint64, need int, pr nttPrime) []uint64 {
	n := 1
	for n < need {
		n <<= 1
	}
	fa := make([]uint64, n)
	fb := make([]uint64, n)
	copy(fa, a)
	copy(fb, b)
	ntt(fa, false, pr.p, pr.g)
	ntt(fb, false, pr.p, pr.g)
	for i := range fa {
		fa[i] = fa[i] * fb[i] % pr.p
	}
	ntt(fa, true, pr.p, pr.g)
	return fa[:need]
}
