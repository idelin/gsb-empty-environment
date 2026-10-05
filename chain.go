package conv

import (
	"fmt"
	"math/big"
	"sync"
)

// primePool lazily generates 31-bit primes descending from 2^31-1.
// All primes are < 2^31, so a product of two residues is < 2^62 and a
// multiply-accumulate step fits in a uint64 with headroom.
var primePool = struct {
	sync.Mutex
	primes []uint32
	next   int64
}{next: 2147483647}

// primeAt returns the i-th pool prime, generating more on demand.
func primeAt(i int) (uint32, bool) {
	primePool.Lock()
	defer primePool.Unlock()
	for len(primePool.primes) <= i {
		if primePool.next <= 1<<30 {
			return 0, false
		}
		n := primePool.next
		primePool.next -= 2
		if big.NewInt(n).ProbablyPrime(20) {
			primePool.primes = append(primePool.primes, uint32(n))
		}
	}
	return primePool.primes[i], true
}

// chain is a set of primes with precomputed Garner CRT constants.
type chain struct {
	primes []uint32   // p_i
	bigP   []*big.Int // p_i as big.Int
	prefix []*big.Int // prefix[i] = p_0 * ... * p_{i-1}; prefix[0] = 1
	inv    []uint32   // inv[i] = prefix[i]^{-1} mod p_i
	mod    *big.Int   // M = product of all p_i
	half   *big.Int   // floor(M/2)
}

// newChain builds the shortest chain with M > 2*bound. It returns an
// error rather than under-provisioning the modulus, so reconstruction
// can never silently wrap around.
func newChain(bound *big.Int) (*chain, error) {
	limit := new(big.Int).Lsh(bound, 1) // 2*bound
	c := &chain{mod: big.NewInt(1)}
	for c.mod.Cmp(limit) <= 0 {
		i := len(c.primes)
		p, ok := primeAt(i)
		if !ok {
			return nil, fmt.Errorf("conv: prime pool exhausted; cannot guarantee M > 2*bound (bound=%s)", bound)
		}
		pre := new(big.Int).Set(c.mod)
		bp := big.NewInt(int64(p))
		inv := new(big.Int).ModInverse(pre, bp)
		if inv == nil {
			return nil, fmt.Errorf("conv: %d not invertible mod %d", pre, p)
		}
		c.primes = append(c.primes, p)
		c.bigP = append(c.bigP, bp)
		c.prefix = append(c.prefix, pre)
		c.inv = append(c.inv, uint32(inv.Uint64()))
		c.mod.Mul(c.mod, bp)
	}
	c.half = new(big.Int).Rsh(c.mod, 1)
	return c, nil
}

// plan holds per-prime reduced coefficients for one product.
type plan struct {
	chain *chain
	amod  [][]uint32 // amod[pi][i] = a_i mod p_pi
	bmod  [][]uint32
	asup  [][]int // support (nonzero indices) of amod[pi]
}

func newPlan(c *chain, a, b *Poly, da, db int) *plan {
	pl := &plan{chain: c}
	t := new(big.Int)
	for _, p := range c.primes {
		m := big.NewInt(int64(p))
		am := make([]uint32, da+1)
		var sup []int
		for i := 0; i <= da; i++ {
			if a.Coeffs[i] == nil {
				continue
			}
			t.Mod(a.Coeffs[i], m) // Mod yields the non-negative residue
			am[i] = uint32(t.Uint64())
			if am[i] != 0 {
				sup = append(sup, i)
			}
		}
		bm := make([]uint32, db+1)
		for j := 0; j <= db; j++ {
			if b.Coeffs[j] == nil {
				continue
			}
			t.Mod(b.Coeffs[j], m)
			bm[j] = uint32(t.Uint64())
		}
		pl.amod = append(pl.amod, am)
		pl.bmod = append(pl.bmod, bm)
		pl.asup = append(pl.asup, sup)
	}
	return pl
}

// convolveBlock computes the exact coefficients of degrees [blo, bhi)
// into out (len bhi-blo), using only index pairs with i+j in range.
func (pl *plan) convolveBlock(db, blo, bhi int, out []*big.Int) {
	n := bhi - blo
	c := pl.chain
	acc := make([]uint32, n)
	r := make([]*big.Int, n)
	for k := range r {
		r[k] = new(big.Int)
	}
	tmp := new(big.Int)
	for pi, p := range c.primes {
		for k := range acc {
			acc[k] = 0
		}
		p64 := uint64(p)
		am, bm := pl.amod[pi], pl.bmod[pi]
		for _, i := range pl.asup[pi] {
			ai := uint64(am[i])
			jlo := maxInt(blo-i, 0)
			jhi := minInt(bhi-1-i, db)
			base := i - blo // out index of (i, j) is base+j
			for j := jlo; j <= jhi; j++ {
				bj := uint64(bm[j])
				if bj == 0 {
					continue
				}
				k := base + j
				acc[k] = uint32((uint64(acc[k]) + ai*bj) % p64)
			}
		}
		// Garner update: r += prefix[pi] * t with
		// t = (acc - r) * inv[pi] mod p.
		bp, pre := c.bigP[pi], c.prefix[pi]
		inv := int64(c.inv[pi])
		pi64 := int64(p)
		for k := 0; k < n; k++ {
			rm := tmp.Mod(r[k], bp).Int64()
			t := (int64(acc[k]) - rm) % pi64
			if t < 0 {
				t += pi64
			}
			t = t * inv % pi64
			if t != 0 {
				r[k].Add(r[k], tmp.Mul(pre, big.NewInt(t)))
			}
		}
	}
	// Symmetric reconstruction: unique representative in (-M/2, M/2].
	for k := 0; k < n; k++ {
		if r[k].Cmp(c.half) > 0 {
			r[k].Sub(r[k], c.mod)
		}
		out[k] = r[k]
	}
}
