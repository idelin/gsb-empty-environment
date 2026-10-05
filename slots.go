package main

import (
	"math/big"
	"sort"
)

// binEval is the exact result of optimally placing one bin's items
// into discrete slots.
type binEval struct {
	ok    bool
	ecc   *big.Rat // minimal achievable eccentricity |cog - target|
	cog   *big.Rat // center of gravity achieving ecc
	slots []int    // slot index per item, parallel to the weights argument
}

// evalBin assigns each item to a distinct slot so that the bin's center of
// gravity lies in [lo, hi], minimizing |cog - target|. All arithmetic is
// exact rational arithmetic; branch-and-bound bounds come from the
// rearrangement inequality over remaining weights and free slots.
func evalBin(weights, positions []*big.Rat, lo, hi, target *big.Rat) binEval {
	r := len(weights)
	m := len(positions)
	if r == 0 || r > m {
		return binEval{}
	}

	ord := make([]int, r)
	for i := range ord {
		ord[i] = i
	}
	sort.Slice(ord, func(a, b int) bool {
		return weights[ord[a]].Cmp(weights[ord[b]]) > 0
	})
	w := make([]*big.Rat, r)
	for i, o := range ord {
		w[i] = weights[o]
	}
	pos := make([]*big.Rat, m)
	copy(pos, positions)
	sort.Slice(pos, func(a, b int) bool { return pos[a].Cmp(pos[b]) < 0 })

	total := new(big.Rat)
	for _, x := range w {
		total.Add(total, x)
	}

	var best binEval
	var bestEcc *big.Rat
	used := make([]bool, m)
	slots := make([]int, r)
	num := new(big.Rat) // weighted position sum of placed items

	var rec func(t int)
	rec = func(t int) {
		if t == r {
			cog := new(big.Rat).Quo(num, total)
			if cog.Cmp(lo) < 0 || cog.Cmp(hi) > 0 {
				return
			}
			ecc := new(big.Rat).Sub(cog, target)
			ecc.Abs(ecc)
			if bestEcc == nil || ecc.Cmp(bestEcc) < 0 {
				bestEcc = ecc
				best.ok = true
				best.ecc = ecc
				best.cog = cog
				best.slots = make([]int, r)
				copy(best.slots, slots)
			}
			return
		}
		// Bound the achievable center of gravity for this branch:
		// pair the heaviest remaining weights with the smallest /
		// largest free slot coordinates.
		k := r - t
		small := make([]*big.Rat, 0, k)
		large := make([]*big.Rat, 0, k)
		for j := 0; j < m && len(small) < k; j++ {
			if !used[j] {
				small = append(small, pos[j])
			}
		}
		for j := m - 1; j >= 0 && len(large) < k; j-- {
			if !used[j] {
				large = append(large, pos[j])
			}
		}
		minAdd := new(big.Rat)
		maxAdd := new(big.Rat)
		for i := 0; i < k; i++ {
			minAdd.Add(minAdd, new(big.Rat).Mul(w[t+i], small[i]))
			maxAdd.Add(maxAdd, new(big.Rat).Mul(w[t+i], large[i]))
		}
		loB := new(big.Rat).Add(num, minAdd)
		loB.Quo(loB, total)
		hiB := new(big.Rat).Add(num, maxAdd)
		hiB.Quo(hiB, total)
		if hiB.Cmp(lo) < 0 || loB.Cmp(hi) > 0 {
			return
		}
		if bestEcc != nil {
			a := loB
			if a.Cmp(lo) < 0 {
				a = lo
			}
			b := hiB
			if b.Cmp(hi) > 0 {
				b = hi
			}
			d := new(big.Rat)
			switch {
			case target.Cmp(a) < 0:
				d.Sub(a, target)
			case target.Cmp(b) > 0:
				d.Sub(target, b)
			}
			if d.Cmp(bestEcc) >= 0 {
				return
			}
		}
		for j := 0; j < m; j++ {
			if used[j] {
				continue
			}
			// Skip duplicate coordinates: using the j-th copy while an
			// identical earlier copy is free yields an equivalent layout.
			if j > 0 && pos[j].Cmp(pos[j-1]) == 0 && !used[j-1] {
				continue
			}
			used[j] = true
			slots[t] = j
			delta := new(big.Rat).Mul(w[t], pos[j])
			num.Add(num, delta)
			rec(t + 1)
			num.Sub(num, delta)
			used[j] = false
		}
	}
	rec(0)

	if best.ok {
		// Map slot choices back to the original weights argument order.
		orig := make([]int, r)
		for i, o := range ord {
			orig[o] = best.slots[i]
		}
		best.slots = orig
	}
	return best
}
