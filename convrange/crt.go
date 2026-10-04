package convrange

import "math/big"

// crtState holds precomputed Garner constants for a fixed prime set.
type crtState struct {
	primes []uint64
	prefix []*big.Int // prefix[j] = p_0*...*p_{j-1}; prefix[len] = M
	inv    []*big.Int // inv[j] = (prefix[j] mod p_j)^-1 mod p_j
	M      *big.Int
}

func newCRT(primes []uint64) *crtState {
	s := &crtState{primes: primes}
	s.prefix = make([]*big.Int, len(primes)+1)
	s.prefix[0] = big.NewInt(1)
	for j, p := range primes {
		s.prefix[j+1] = new(big.Int).Mul(s.prefix[j], big.NewInt(int64(p)))
	}
	s.M = s.prefix[len(primes)]
	s.inv = make([]*big.Int, len(primes))
	s.inv[0] = big.NewInt(0) // unused
	for j := 1; j < len(primes); j++ {
		pj := big.NewInt(int64(primes[j]))
		s.inv[j] = new(big.Int).ModInverse(s.prefix[j], pj)
	}
	return s
}

// lift combines residues r[j] = x mod primes[j] via Garner's algorithm and
// returns the centered representative in [-(M-1)/2, (M-1)/2]. Exactness of
// this representative is guaranteed by the bound argument in the package
// documentation: the true coefficient lies in the same interval.
func (s *crtState) lift(r []uint64) *big.Int {
	x := big.NewInt(int64(r[0]))
	for j := 1; j < len(s.primes); j++ {
		pj := int64(s.primes[j])
		// t = (r_j - x) * inv_j mod p_j
		t := new(big.Int).Sub(big.NewInt(int64(r[j])), x)
		t.Mul(t, s.inv[j])
		t.Mod(t, big.NewInt(pj))
		// x += prefix_j * t  (== x mod p_i for i<j, == r_j mod p_j)
		x.Add(x, t.Mul(t, s.prefix[j]))
	}
	// Centered lift: if 2x > M then x -= M.
	if new(big.Int).Lsh(x, 1).Cmp(s.M) > 0 {
		x.Sub(x, s.M)
	}
	return x
}
