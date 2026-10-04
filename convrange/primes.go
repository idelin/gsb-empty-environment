package convrange

// prevPrime returns the largest prime < n. Used to enumerate 31-bit primes
// downward from 2^31-1; the supply is effectively inexhaustible here
// (thousands of primes available within a few percent below 2^31).
func prevPrime(n uint64) uint64 {
	if n%2 == 0 {
		n--
	}
	for ; ; n -= 2 {
		if isPrime31(n) {
			return n
		}
	}
}

// isPrime31 is deterministic Miller-Rabin for n < 2^32 (bases 2, 7, 61).
func isPrime31(n uint64) bool {
	if n < 2 {
		return false
	}
	for _, p := range []uint64{2, 3, 5, 7, 61} {
		if n == p {
			return true
		}
		if n%p == 0 {
			return false
		}
	}
	d := n - 1
	s := 0
	for d%2 == 0 {
		d /= 2
		s++
	}
	for _, a := range []uint64{2, 7, 61} {
		if a%n == 0 {
			continue
		}
		x := powMod(a, d, n)
		if x == 1 || x == n-1 {
			continue
		}
		composite := true
		for r := 1; r < s; r++ {
			x = x * x % n
			if x == n-1 {
				composite = false
				break
			}
		}
		if composite {
			return false
		}
	}
	return true
}

// powMod computes base^exp mod m. All values are < 2^31, so products fit
// in uint64 with room to spare.
func powMod(base, exp, m uint64) uint64 {
	result := uint64(1)
	base %= m
	for exp > 0 {
		if exp&1 == 1 {
			result = result * base % m
		}
		base = base * base % m
		exp >>= 1
	}
	return result
}
