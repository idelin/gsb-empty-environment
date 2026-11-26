package admission

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"sync"
)

// I is a small convenience constructor for big integers.
func I(v int64) *big.Int { return big.NewInt(v) }

// MustInt parses a decimal integer and panics on malformed input.  It is
// intended for package users constructing static policies.
func MustInt(s string) *big.Int {
	z, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic(fmt.Sprintf("admission: invalid integer %q", s))
	}
	return z
}

func cloneInt(z *big.Int) *big.Int {
	if z == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(z)
}

func cloneInts(zs []*big.Int) []*big.Int {
	out := make([]*big.Int, len(zs))
	for i, z := range zs {
		out[i] = cloneInt(z)
	}
	return out
}

func modInt(z, m *big.Int) *big.Int {
	if m.Sign() <= 0 {
		return cloneInt(z)
	}
	r := new(big.Int).Mod(z, m)
	if r.Sign() < 0 {
		r.Add(r, m)
	}
	return r
}

func gcdInt(a, b *big.Int) *big.Int {
	return new(big.Int).GCD(nil, nil, new(big.Int).Abs(a), new(big.Int).Abs(b))
}

func powInt(base *big.Int, exp int) *big.Int {
	return new(big.Int).Exp(base, big.NewInt(int64(exp)), nil)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func egcd(a, b *big.Int) (g, x, y *big.Int) {
	g = new(big.Int)
	x = new(big.Int)
	y = new(big.Int)
	g.GCD(x, y, new(big.Int).Abs(a), new(big.Int).Abs(b))
	if a.Sign() < 0 {
		x.Neg(x)
	}
	if b.Sign() < 0 {
		y.Neg(y)
	}
	return g, x, y
}

func modInverse(z, m *big.Int) (*big.Int, error) {
	zz := modInt(z, m)
	g, x, _ := egcd(zz, m)
	if g.Cmp(big.NewInt(1)) != 0 {
		return nil, fmt.Errorf("admission: %s is not invertible modulo %s", z, m)
	}
	return modInt(x, m), nil
}

// crt2 combines r1 (mod m1) and r2 (mod m2), assuming gcd(m1,m2)=1.
func crt2(r1, m1, r2, m2 *big.Int) (*big.Int, *big.Int, error) {
	inv, err := modInverse(modInt(m1, m2), m2)
	if err != nil {
		return nil, nil, err
	}
	diff := new(big.Int).Sub(modInt(r2, m2), modInt(r1, m2))
	diff.Mod(diff, m2)
	t := new(big.Int).Mul(diff, inv)
	t.Mod(t, m2)
	x := new(big.Int).Add(r1, new(big.Int).Mul(m1, t))
	m := new(big.Int).Mul(m1, m2)
	x.Mod(x, m)
	return x, m, nil
}

// ---------------------------------------------------------------------------
// Integer factorisation used for CRT decomposition.
// ---------------------------------------------------------------------------

var factorCache sync.Map // string -> map[string]int

var smallPrimes = []int64{
	2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53,
	59, 61, 67, 71, 73, 79, 83, 89, 97, 101, 103, 107, 109, 113,
	127, 131, 137, 139, 149, 151, 157, 163, 167, 173, 179, 181,
	191, 193, 197, 199, 211, 223, 227, 229, 233, 239, 241, 251,
	257, 263, 269, 271, 277, 281, 283, 293, 307, 311, 313, 317,
	331, 337, 347, 349, 353, 359, 367, 373, 379, 383, 389, 397,
	401, 409, 419, 421, 431, 433, 439, 443, 449, 457, 461, 463,
	467, 479, 487, 491, 499, 503, 509, 521, 523, 541, 547, 557,
	563, 569, 571, 577, 587, 593, 599, 601, 607, 613, 617, 619,
	631, 641, 643, 647, 653, 659, 661, 673, 677, 683, 691, 701,
	709, 719, 727, 733, 739, 743, 751, 757, 761, 769, 773, 787,
	797, 809, 811, 821, 823, 827, 829, 839, 853, 857, 859, 863,
	877, 881, 883, 887, 907, 911, 919, 929, 937, 941, 947, 953,
	967, 971, 977, 983, 991, 997,
}

func factorize(z *big.Int) (map[string]int, error) {
	if z == nil || z.Sign() <= 0 {
		return nil, errors.New("admission: factorization requires a positive integer")
	}
	n := new(big.Int).Set(z)
	key := n.String()
	if hit, ok := factorCache.Load(key); ok {
		orig := hit.(map[string]int)
		out := make(map[string]int, len(orig))
		for p, e := range orig {
			out[p] = e
		}
		return out, nil
	}
	res := make(map[string]int)
	for _, p := range smallPrimes {
		bp := big.NewInt(p)
		for {
			q, r := new(big.Int).QuoRem(n, bp, new(big.Int))
			if r.Sign() != 0 {
				break
			}
			n = q
			res[bp.String()]++
		}
		if n.Cmp(big.NewInt(1)) == 0 {
			break
		}
	}
	if n.Cmp(big.NewInt(1)) > 0 {
		if err := factorRec(n, res); err != nil {
			return nil, err
		}
	}
	factorCache.Store(key, res)
	out := make(map[string]int, len(res))
	for p, e := range res {
		out[p] = e
	}
	return out, nil
}

func factorRec(n *big.Int, out map[string]int) error {
	if n.Cmp(big.NewInt(1)) == 0 {
		return nil
	}
	if isPrime(n) {
		out[n.String()]++
		return nil
	}
	d, err := pollardRho(n)
	if err != nil {
		return err
	}
	if d.Cmp(n) == 0 {
		return fmt.Errorf("admission: failed to factor %s", n)
	}
	if err := factorRec(new(big.Int).Set(d), out); err != nil {
		return err
	}
	return factorRec(new(big.Int).Quo(n, d), out)
}

func isPrime(n *big.Int) bool {
	if n.Sign() <= 0 {
		return false
	}
	if n.Cmp(big.NewInt(2)) < 0 {
		return false
	}
	if n.Cmp(big.NewInt(2)) == 0 {
		return true
	}
	if n.Bit(0) == 0 {
		return false
	}
	// Deterministic for 64-bit inputs; a high-probability test is used for
	// unusually large user-supplied release moduli.
	if n.BitLen() <= 64 {
		return deterministicMR64(n)
	}
	return n.ProbablyPrime(32)
}

func deterministicMR64(n *big.Int) bool {
	// Bases sufficient for all n < 2^64.
	bases := []int64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37}
	nm1 := new(big.Int).Sub(n, big.NewInt(1))
	d := new(big.Int).Set(nm1)
	s := 0
	for d.Bit(0) == 0 {
		d.Rsh(d, 1)
		s++
	}
	for _, b := range bases {
		a := big.NewInt(b)
		if a.Cmp(n) >= 0 {
			continue
		}
		x := new(big.Int).Exp(a, d, n)
		if x.Cmp(big.NewInt(1)) == 0 || x.Cmp(nm1) == 0 {
			continue
		}
		composite := true
		for r := 1; r < s; r++ {
			x.Mul(x, x).Mod(x, n)
			if x.Cmp(nm1) == 0 {
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

func pollardRho(n *big.Int) (*big.Int, error) {
	if n.Bit(0) == 0 {
		return big.NewInt(2), nil
	}
	rng := rand.New(rand.NewSource(0x5eed))
	two := big.NewInt(2)
	one := big.NewInt(1)
	limit := 1 << 20
	for attempt := 0; attempt < 256; attempt++ {
		c := new(big.Int).Rand(rng, new(big.Int).Sub(n, two))
		c.Add(c, one)
		x := new(big.Int).Rand(rng, n)
		y := new(big.Int).Set(x)
		d := new(big.Int).Set(one)
		for iter := 0; iter < limit && d.Cmp(one) == 0; iter++ {
			x.Mul(x, x).Add(x, c).Mod(x, n)
			y.Mul(y, y).Add(y, c).Mod(y, n)
			y.Mul(y, y).Add(y, c).Mod(y, n)
			diff := new(big.Int).Sub(x, y)
			diff.Abs(diff)
			d.GCD(nil, nil, diff, n)
		}
		if d.Cmp(one) > 0 && d.Cmp(n) < 0 {
			return d, nil
		}
	}
	return nil, fmt.Errorf("admission: Pollard rho failed on %s", n)
}

func sortedPrimeStrings(set map[string]bool) []string {
	ps := make([]string, 0, len(set))
	for p := range set {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool {
		a, _ := new(big.Int).SetString(ps[i], 10)
		b, _ := new(big.Int).SetString(ps[j], 10)
		return a.Cmp(b) < 0
	})
	return ps
}
