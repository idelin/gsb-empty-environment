package prefixcode

import (
	"fmt"
	"sort"
)

// BuildLengths computes optimal prefix-code lengths for the given symbol
// frequencies, with every code length bounded by maxLen. It uses the
// package-merge algorithm, which is exact (not a heuristic): the resulting
// lengths minimize sum(freq[i] * length[i]) subject to the Kraft equality
// and length[i] <= maxLen.
//
// Symbols with zero frequency get length 0 (no code). A single used symbol
// gets length 1 (a real 1-bit code, never an undecodable empty code).
// Among symbols with equal frequency the tie is broken by byte order:
// the smaller symbol value receives a length no larger than the bigger one.
// The result is deterministic: identical inputs always yield identical
// lengths.
func BuildLengths(freqs []uint64, maxLen int) ([]int, error) {
	if maxLen < 1 || maxLen > MaxMaxLen {
		return nil, fmt.Errorf("%v: %d", ErrMaxLen, maxLen)
	}
	lengths := make([]int, len(freqs))
	type weighted struct {
		sym  int
		freq uint64
	}
	var used []weighted
	for sym, f := range freqs {
		if f > 0 {
			used = append(used, weighted{sym, f})
		}
	}
	n := len(used)
	switch n {
	case 0:
		return lengths, nil
	case 1:
		lengths[used[0].sym] = 1
		return lengths, nil
	}
	if uint64(n) > uint64(1)<<uint(maxLen) {
		return nil, fmt.Errorf("%v: %d symbols cannot fit in %d bits", ErrMaxLen, n, maxLen)
	}
	sort.Slice(used, func(i, j int) bool {
		if used[i].freq != used[j].freq {
			return used[i].freq < used[j].freq
		}
		return used[i].sym < used[j].sym
	})

	// Package-merge. Each item records how many times every leaf occurs
	// in it (a leaf may occur multiple times in one package); a leaf's
	// code length equals the total number of occurrences of that leaf
	// among the first 2n-2 items of the final list.
	type item struct {
		weight  uint64
		members []uint32
	}
	leaves := make([]item, n)
	for i, u := range used {
		m := make([]uint32, len(freqs))
		m[u.sym] = 1
		leaves[i] = item{weight: u.freq, members: m}
	}
	limit := 2*n - 2
	list := leaves
	for level := 1; level < maxLen; level++ {
		packs := make([]item, 0, len(list)/2)
		for i := 0; i+1 < len(list); i += 2 {
			a, b := list[i], list[i+1]
			m := make([]uint32, len(freqs))
			for k := range m {
				m[k] = a.members[k] + b.members[k]
			}
			packs = append(packs, item{weight: a.weight + b.weight, members: m})
		}
		merged := make([]item, 0, limit)
		li, pi := 0, 0
		for len(merged) < limit && (li < len(leaves) || pi < len(packs)) {
			if pi >= len(packs) || (li < len(leaves) && leaves[li].weight <= packs[pi].weight) {
				merged = append(merged, leaves[li])
				li++
			} else {
				merged = append(merged, packs[pi])
				pi++
			}
		}
		list = merged
	}
	if len(list) < limit {
		return nil, fmt.Errorf("%v: %d symbols cannot fit in %d bits", ErrMaxLen, n, maxLen)
	}
	for _, it := range list[:limit] {
		for sym, cnt := range it.members {
			lengths[sym] += int(cnt)
		}
	}

	// Tie-break by byte order: within each equal-frequency group, assign
	// the sorted lengths to the symbols in ascending symbol order. This
	// keeps the cost unchanged (frequencies are equal) and makes the
	// result canonical: smaller byte value => shorter-or-equal code.
	for i := 0; i < n; {
		j := i
		for j < n && freqs[used[j].sym] == freqs[used[i].sym] {
			j++
		}
		ls := make([]int, 0, j-i)
		for k := i; k < j; k++ {
			ls = append(ls, lengths[used[k].sym])
		}
		sort.Ints(ls)
		for k := i; k < j; k++ {
			lengths[used[k].sym] = ls[k-i]
		}
		i = j
	}
	return lengths, nil
}
