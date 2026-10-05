package main

import (
	"math/big"
	"sort"
)

// binSol is the solved layout of one bin.
type binSol struct {
	items  []int    // indices into Instance.Items
	slots  []int    // slot per item, parallel to items
	weight *big.Rat // total weight
	cog    *big.Rat // exact center of gravity
	ecc    *big.Rat // |cog - target|
}

// solution is a complete placement.
type solution struct {
	bins   []binSol
	maxEcc *big.Rat
}

type solver struct {
	in   *Instance
	ord  []int      // item indices sorted by weight descending
	w    []*big.Rat // weights in that order
	n    int
	full uint32

	bestBins int
	bestEcc  *big.Rat
	best     *solution

	evalCache map[uint32]binEval
}

// solve finds a placement minimizing (number of bins, max eccentricity)
// lexicographically. Returns nil if no feasible placement exists.
func solve(in *Instance) *solution {
	n := len(in.Items)
	s := &solver{
		in:        in,
		n:         n,
		full:      uint32(1)<<uint(n) - 1,
		bestBins:  n + 1,
		evalCache: map[uint32]binEval{},
	}
	s.ord = make([]int, n)
	for i := range s.ord {
		s.ord[i] = i
	}
	sort.Slice(s.ord, func(a, b int) bool {
		c := in.Items[s.ord[a]].Weight.Cmp(in.Items[s.ord[b]].Weight)
		if c != 0 {
			return c > 0
		}
		return in.Items[s.ord[a]].ID < in.Items[s.ord[b]].ID
	})
	s.w = make([]*big.Rat, n)
	for i, o := range s.ord {
		s.w[i] = in.Items[o].Weight
	}

	var bins []binSol
	s.search(s.full, nil, &bins)
	return s.best
}

// maskWeight sums weights of sorted items selected by mask.
func (s *solver) maskWeight(mask uint32) *big.Rat {
	t := new(big.Rat)
	for i := 0; i < s.n; i++ {
		if mask&(1<<uint(i)) != 0 {
			t.Add(t, s.w[i])
		}
	}
	return t
}

// ceilDivRat returns ceil(a/b) for positive rationals as an int.
func ceilDivRat(a, b *big.Rat) int {
	q := new(big.Rat).Quo(a, b)
	num := q.Num()
	den := q.Denom()
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	return int(quo.Int64())
}

type subsetCand struct {
	mask uint32
	ev   binEval
}

// evalSubset evaluates (with caching) the optimal slot layout for the bin
// formed by the sorted items in mask.
func (s *solver) evalSubset(mask uint32) binEval {
	if ev, ok := s.evalCache[mask]; ok {
		return ev
	}
	var ws []*big.Rat
	for i := 0; i < s.n; i++ {
		if mask&(1<<uint(i)) != 0 {
			ws = append(ws, s.w[i])
		}
	}
	ev := evalBin(ws, s.in.Positions, s.in.SafeLo, s.in.SafeHi, s.in.Target)
	s.evalCache[mask] = ev
	return ev
}

// genSubsets enumerates subsets of remaining that contain its lowest set
// bit, respect capacity, and admit a feasible slot layout. Items of equal
// weight are interchangeable, so only one representative per equal-weight
// group is expanded at each level.
func (s *solver) genSubsets(remaining uint32) []subsetCand {
	lowIdx := 0
	for i := 0; i < s.n; i++ {
		if remaining&(1<<uint(i)) != 0 {
			lowIdx = i
			break
		}
	}
	var cands []subsetCand
	var rec func(mask uint32, next int, w *big.Rat)
	rec = func(mask uint32, next int, w *big.Rat) {
		if ev := s.evalSubset(mask); ev.ok {
			cands = append(cands, subsetCand{mask: mask, ev: ev})
		}
		var prevW *big.Rat
		for j := next; j < s.n; j++ {
			bit := uint32(1) << uint(j)
			if remaining&bit == 0 {
				continue
			}
			if prevW != nil && prevW.Cmp(s.w[j]) == 0 {
				continue
			}
			nw := new(big.Rat).Add(w, s.w[j])
			if nw.Cmp(s.in.Capacity) > 0 {
				prevW = s.w[j]
				continue
			}
			prevW = s.w[j]
			rec(mask|bit, j+1, nw)
		}
	}
	rec(uint32(1)<<uint(lowIdx), lowIdx+1, new(big.Rat).Set(s.w[lowIdx]))
	// Larger bins first (drives bin count down early), then lower
	// eccentricity for a strong incumbent.
	sort.Slice(cands, func(a, b int) bool {
		ca, cb := popcount(cands[a].mask), popcount(cands[b].mask)
		if ca != cb {
			return ca > cb
		}
		return cands[a].ev.ecc.Cmp(cands[b].ev.ecc) < 0
	})
	return cands
}

func popcount(x uint32) int {
	c := 0
	for x != 0 {
		x &= x - 1
		c++
	}
	return c
}

// search packs the items in remaining into bins, one bin at a time.
// maxEcc is the worst eccentricity among bins already fixed (nil if none).
func (s *solver) search(remaining uint32, maxEcc *big.Rat, bins *[]binSol) {
	if remaining == 0 {
		used := len(*bins)
		if used < s.bestBins || (used == s.bestBins &&
			(s.bestEcc == nil || maxEcc.Cmp(s.bestEcc) < 0)) {
			s.bestBins = used
			s.bestEcc = new(big.Rat).Set(maxEcc)
			cp := make([]binSol, used)
			copy(cp, *bins)
			s.best = &solution{bins: cp, maxEcc: s.bestEcc}
		}
		return
	}
	used := len(*bins)
	lb := ceilDivRat(s.maskWeight(remaining), s.in.Capacity)
	if used+lb > s.bestBins {
		return
	}
	if s.bestEcc != nil && maxEcc != nil && used+lb >= s.bestBins &&
		maxEcc.Cmp(s.bestEcc) >= 0 {
		return
	}
	for _, cand := range s.genSubsets(remaining) {
		nm := maxEcc
		if nm == nil || cand.ev.ecc.Cmp(nm) > 0 {
			nm = cand.ev.ecc
		}
		var items []int
		for i := 0; i < s.n; i++ {
			if cand.mask&(1<<uint(i)) != 0 {
				items = append(items, s.ord[i])
			}
		}
		// evalSubset built weights in sorted order; slots align with it.
		bin := binSol{
			items:  items,
			slots:  cand.ev.slots,
			weight: s.maskWeight(cand.mask),
			cog:    cand.ev.cog,
			ecc:    cand.ev.ecc,
		}
		*bins = append(*bins, bin)
		s.search(remaining&^cand.mask, nm, bins)
		*bins = (*bins)[:len(*bins)-1]
	}
}
