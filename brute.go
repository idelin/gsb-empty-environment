package main

import (
	"math/big"
)

// bruteForce enumerates all 2^n file subsets (n <= 15 by contract) and
// returns the optimum under the same (cost, sorted-ID-list) ordering as the
// solver. Used to cross-check the branch-and-bound solver in tests.
func bruteForce(e *env) (cost *big.Int, ids []string, ok bool) {
	n := e.n
	var bestCost *big.Int
	var bestIDs []string
	for bits := uint64(0); bits < (uint64(1) << uint(n)); bits++ {
		mask := newBitmap(n)
		for i := 0; i < n; i++ {
			if bits&(uint64(1)<<uint(i)) != 0 {
				mask.set(i)
			}
		}
		if !feasibleSet(e, mask) {
			continue
		}
		c := e.costOfMask(mask)
		ids := e.idsOf(mask)
		if bestCost == nil || solLess(c, ids, bestCost, bestIDs) {
			bestCost = c
			bestIDs = ids
		}
	}
	if bestCost == nil {
		return nil, nil, false
	}
	return bestCost, bestIDs, true
}

// feasibleSet is the ground-truth feasibility predicate shared by the brute
// force cross-check: only usable files, dependency-closed, and every shard
// reachable from 0 to its target.
func feasibleSet(e *env, mask bitmap) bool {
	for i := 0; i < e.n; i++ {
		if mask.get(i) && !e.usable[i] {
			return false
		}
	}
	for i := 0; i < e.n; i++ {
		if mask.get(i) && !e.deps[i].subsetOf(mask) {
			return false
		}
	}
	for _, sh := range e.shards {
		if !shardReachable(sh, mask) {
			return false
		}
	}
	return true
}
