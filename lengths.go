// Package llex implements a length-limited prefix-code encoder with a
// proof of optimality, canonical code-table serialization, and a chunked
// file encoder whose block layout is chosen by an exact dynamic program.
//
// The core solver uses the package-merge algorithm (Larmore & Hirschberg),
// never "Huffman then truncate". All frequencies and costs are exact
// integers (math/big), so arbitrarily large frequencies are handled.
//
// Optimality criterion: minimize the weighted code length
//
//	sum_i freq_i * len_i
//
// subject to 1 <= len_i <= L and the Kraft inequality. Among all
// minimum-cost solutions the lexicographically smallest length vector
// (symbols ordered by ID) is selected. This tie-break is folded into the
// solver by exact symbolic perturbation (see SolveLengths), so the
// returned vector is the unique minimizer of a single exact objective.
package llex

import (
	"fmt"
	"math/big"
	"sort"
)

// Symbol is an input symbol with a stable ID and a non-negative frequency.
type Symbol struct {
	ID   string
	Freq uint64
}

// Table describes a prefix code by code lengths. IDs are sorted ascending
// and Lengths[i] is the code length of IDs[i]. L is the effective maximum
// code length (clamped to what any optimal code can need, see SolveLengths).
type Table struct {
	L       int
	IDs     []string
	Lengths []int
}

// InfeasibleError is returned when the alphabet cannot fit into L bits.
// It carries the evidence: n symbols each need a codeword of length <= L,
// so the Kraft sum would be at least n * 2^-L > 1.
type InfeasibleError struct {
	N   int    // number of symbols
	L   int    // maximum allowed code length
	Cap string // 2^L in decimal, the maximum number of codewords
}

func (e *InfeasibleError) Error() string {
	return fmt.Sprintf("llex: infeasible: %d symbols cannot all receive lengths in [1,%d]: "+
		"a prefix code with maximum length %d has at most 2^%d = %s codewords, "+
		"so the Kraft sum would be at least %d * 2^-%d > 1",
		e.N, e.L, e.L, e.L, e.Cap, e.N, e.L)
}

// effectiveL clamps L to the deepest length any optimal complete code can
// use: a complete prefix code with n leaves has depth at most n-1. This
// keeps the solver O(n * min(L, n)) even for astronomically large L and
// never enumerates the 2^L leaves.
func effectiveL(n, L int) int {
	maxDepth := n - 1
	if maxDepth < 1 {
		maxDepth = 1
	}
	if L > maxDepth {
		return maxDepth
	}
	return L
}

// SolveLengths computes the optimal length-limited code for the given
// symbols. The returned table lists every input symbol exactly once
// (zero-frequency symbols included) with lengths in [1, L].
//
// Special cases: an empty alphabet yields an empty table; a single symbol
// always gets length 1. If n > 2^L the error is an *InfeasibleError with
// the Kraft-sum evidence.
//
// The tie-break is implemented exactly: with B = L+1 and C = B^n, symbol i
// (in ID order) is solved with weight
//
//	w_i = freq_i * C + B^(n-1-i)
//
// Since every length satisfies len_i <= L < B, the sum
//
//	sum_i len_i * w_i = C * (sum_i len_i freq_i) + sum_i len_i B^(n-1-i)
//
// uniquely determines both the weighted cost (high part) and the length
// vector itself (low part, a base-B number). Distinct vectors therefore
// have distinct combined costs, so the unique minimizer of the combined
// objective is exactly the lexicographically smallest optimal vector.
func SolveLengths(syms []Symbol, L int) (*Table, error) {
	if L < 1 {
		return nil, fmt.Errorf("llex: maximum code length L must be >= 1, got %d", L)
	}
	n := len(syms)
	freq := make(map[string]uint64, n)
	for _, s := range syms {
		if _, dup := freq[s.ID]; dup {
			return nil, fmt.Errorf("llex: duplicate symbol ID %q", s.ID)
		}
		freq[s.ID] = s.Freq
	}
	ids := make([]string, 0, n)
	for id := range freq {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	if n == 0 {
		return &Table{L: effectiveL(0, L)}, nil
	}
	if n == 1 {
		return &Table{L: 1, IDs: ids, Lengths: []int{1}}, nil
	}

	leff := effectiveL(n, L)
	if leff < 64 && uint64(n) > uint64(1)<<uint(leff) {
		cap2 := new(big.Int).Lsh(big.NewInt(1), uint(leff))
		return nil, &InfeasibleError{N: n, L: leff, Cap: cap2.String()}
	}

	// Exact perturbed weights, see the comment above.
	b := big.NewInt(int64(leff) + 1)
	c := new(big.Int).Exp(b, big.NewInt(int64(n)), nil)
	w := make([]*big.Int, n)
	for i, id := range ids {
		tie := new(big.Int).Exp(b, big.NewInt(int64(n-1-i)), nil)
		wi := new(big.Int).Mul(new(big.Int).SetUint64(freq[id]), c)
		w[i] = wi.Add(wi, tie)
	}

	lengths, err := packageMerge(w, leff)
	if err != nil {
		return nil, err
	}
	return &Table{L: leff, IDs: ids, Lengths: lengths}, nil
}

// pmItem is a node of the package-merge lists: either an original leaf or
// a package of two items from the previous list.
type pmItem struct {
	w    *big.Int
	leaf int // symbol index in ID order, -1 for packages
	a, b *pmItem
}

// packageMerge solves: minimize sum_i w_i * len_i subject to
// 1 <= len_i <= L and sum_i 2^-len_i <= 1 (the optimum saturates Kraft).
//
// Running time is O(n log n + n*L) big-integer operations: each of the L
// levels merges the sorted leaves with the sorted packages of the previous
// level, both O(n). Space is O(n*L) items. In particular the running time
// is independent of 2^L.
func packageMerge(w []*big.Int, L int) ([]int, error) {
	n := len(w)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return w[order[a]].Cmp(w[order[b]]) < 0 })
	leaves := make([]*pmItem, n)
	for i, s := range order {
		leaves[i] = &pmItem{w: w[s], leaf: s}
	}

	var prev []*pmItem
	for d := 0; d < L; d++ {
		pkgs := make([]*pmItem, 0, len(prev)/2)
		for k := 0; k+1 < len(prev); k += 2 {
			pkgs = append(pkgs, &pmItem{
				w:    new(big.Int).Add(prev[k].w, prev[k+1].w),
				leaf: -1,
				a:    prev[k],
				b:    prev[k+1],
			})
		}
		prev = mergeItems(leaves, pkgs)
	}

	need := 2*n - 2
	if len(prev) < need {
		return nil, fmt.Errorf("llex: internal infeasibility: package-merge produced %d items, need %d", len(prev), need)
	}
	lengths := make([]int, n)
	for _, it := range prev[:need] {
		countLeaves(it, lengths)
	}
	return lengths, nil
}

// mergeItems merges two weight-sorted lists into one weight-sorted list.
func mergeItems(a, b []*pmItem) []*pmItem {
	out := make([]*pmItem, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i].w.Cmp(b[j].w) <= 0 {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

// countLeaves adds the number of occurrences of each original leaf below
// item it to counts; that occurrence count is the symbol's code length.
func countLeaves(it *pmItem, counts []int) {
	if it.leaf >= 0 {
		counts[it.leaf]++
		return
	}
	countLeaves(it.a, counts)
	countLeaves(it.b, counts)
}
