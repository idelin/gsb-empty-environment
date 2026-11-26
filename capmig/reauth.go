package capmig

import (
	"math/big"
	"math/bits"
)

// minimumHittingSet returns the smallest set of candidate indices that
// intersects every cover set. Each cover set is a bit set over the candidate
// list. Branch-and-bound gives an exact optimum; ties resolve toward smaller
// candidate indices for deterministic output.
func minimumHittingSet(candidates []string, sets [][]uint64) []string {
	if len(sets) == 0 {
		return nil
	}
	n := len(candidates)

	bigSets := make([]*big.Int, 0, len(sets))
	seen := map[string]bool{}
	for _, s := range sets {
		bi := bigFromWords(s)
		if bi.Sign() == 0 {
			// Defensive: an empty cover set cannot be hit; fail closed by
			// requiring every candidate.
			bi = new(big.Int)
			for i := 0; i < n; i++ {
				bi.SetBit(bi, i, 1)
			}
		}
		key := bi.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		bigSets = append(bigSets, bi)
	}

	// Candidate frequency drives the branching heuristic.
	freq := make([]int, n)
	for _, s := range bigSets {
		for i := 0; i < n; i++ {
			if s.Bit(i) == 1 {
				freq[i]++
			}
		}
	}
	order := make([]int, 0, n)
	for i := range candidates {
		order = append(order, i)
	}
	sortByFreqThenIndex(order, freq)

	best := new(big.Int)
	for i := 0; i < n; i++ {
		best.SetBit(best, i, 1)
	}
	current := new(big.Int)

	var branch func(remaining []*big.Int)
	branch = func(remaining []*big.Int) {
		curSize := popcountBig(current)
		bestSize := popcountBig(best)
		if curSize >= bestSize {
			return
		}
		if len(remaining) == 0 {
			if curSize < bestSize ||
				(curSize == bestSize && lexicographicSmaller(current, best, n)) {
				best.Set(current)
			}
			return
		}

		// Lower bound: disjoint-set greedy packing.
		if curSize+packingLowerBound(remaining) >= bestSize {
			return
		}

		// Pick the smallest remaining set: branching over its elements is
		// sufficient to hit it.
		target := smallestSet(remaining)
		members := setBits(target, order)
		for _, cand := range members {
			current.SetBit(current, cand, 1)
			next := filterUnHit(remaining, cand)
			branch(next)
			current.SetBit(current, cand, 0)
		}
	}
	branch(bigSets)

	out := []string{}
	for i := 0; i < n; i++ {
		if best.Bit(i) == 1 {
			out = append(out, candidates[i])
		}
	}
	return out
}

func sortByFreqThenIndex(order []int, freq []int) {
	for i := 1; i < len(order); i++ {
		for j := i; j > 0; j-- {
			a, b := order[j-1], order[j]
			if freq[a] < freq[b] || (freq[a] == freq[b] && a > b) {
				order[j-1], order[j] = order[j], order[j-1]
			} else {
				break
			}
		}
	}
}

func smallestSet(sets []*big.Int) *big.Int {
	best := sets[0]
	for _, s := range sets[1:] {
		if popcountBig(s) < popcountBig(best) {
			best = s
		}
	}
	return best
}

func setBits(set *big.Int, order []int) []int {
	var out []int
	for _, i := range order {
		if set.Bit(i) == 1 {
			out = append(out, i)
		}
	}
	return out
}

func filterUnHit(sets []*big.Int, chosen int) []*big.Int {
	var out []*big.Int
	for _, s := range sets {
		if s.Bit(chosen) == 0 {
			out = append(out, s)
		}
	}
	return out
}

// packingLowerBound greedily packs mutually disjoint sets; the packing count
// is a valid lower bound on the hitting-set size.
func packingLowerBound(sets []*big.Int) int {
	used := new(big.Int)
	count := 0
	for _, s := range sets {
		if new(big.Int).And(s, used).Sign() != 0 {
			continue
		}
		used.Or(used, s)
		count++
	}
	return count
}

func popcountBig(x *big.Int) int {
	n := 0
	for _, w := range x.Bits() {
		n += bits.OnesCount64(uint64(w))
	}
	return n
}

func lexicographicSmaller(a, b *big.Int, n int) bool {
	for i := 0; i < n; i++ {
		if a.Bit(i) != b.Bit(i) {
			return a.Bit(i) == 0
		}
	}
	return false
}
