// Package huffcodec builds deterministic, length-limited canonical prefix
// codes and provides a streaming decoder with an explicit wire format.
//
// Only the Go standard library is used. Code lengths are computed with the
// package-merge algorithm, which produces an exactly optimal prefix code
// under a maximum code length constraint. Ties between equal-frequency
// symbols are broken by byte (symbol) order, so the output is fully
// deterministic.
package huffcodec

import (
	"errors"
	"sort"
)

// MaxCodeLength is the largest supported code length in bits.
const MaxCodeLength = 32

var (
	// ErrMaxBitsTooSmall is returned when maxBits cannot hold the alphabet
	// (need 2^maxBits >= number of symbols) or is out of range.
	ErrMaxBitsTooSmall = errors.New("huffcodec: max code length too small for alphabet")
	// ErrOversubscribed reports a code length set whose Kraft sum exceeds 1.
	ErrOversubscribed = errors.New("huffcodec: oversubscribed code lengths (Kraft sum > 1)")
	// ErrIncomplete reports a code length set whose Kraft sum is below 1.
	ErrIncomplete = errors.New("huffcodec: incomplete code lengths (Kraft sum < 1)")
)

// KraftScaled returns the Kraft sum of the given code lengths scaled by
// 2^MaxCodeLength, i.e. a return value of 1<<MaxCodeLength means exactly 1.0
// (a complete code). Zero lengths (absent symbols) contribute nothing.
func KraftScaled(lengths []uint8) uint64 {
	var sum uint64
	for _, l := range lengths {
		if l == 0 || l > MaxCodeLength {
			continue
		}
		sum += 1 << (MaxCodeLength - l)
	}
	return sum
}

// ValidateLengths checks a code length assignment. A single-symbol code is
// accepted even though its Kraft sum is 1/2, because it is still decodable.
// Empty tables, oversubscribed tables and otherwise incomplete tables are
// rejected with distinct errors.
func ValidateLengths(lengths []uint8) error {
	n := 0
	for _, l := range lengths {
		if l > 0 {
			n++
		}
	}
	if n == 0 {
		return ErrIncomplete
	}
	if n == 1 {
		return nil
	}
	k := KraftScaled(lengths)
	switch {
	case k > 1<<MaxCodeLength:
		return ErrOversubscribed
	case k < 1<<MaxCodeLength:
		return ErrIncomplete
	}
	return nil
}

// Cost returns the weighted cost sum(freqs[i]*lengths[i]) in bits.
func Cost(freqs []uint64, lengths []uint8) uint64 {
	var c uint64
	for i, f := range freqs {
		if i < len(lengths) {
			c += f * uint64(lengths[i])
		}
	}
	return c
}

// CodeLengths computes an exactly optimal length-limited prefix code for the
// given symbol frequencies using the package-merge algorithm. Symbols with
// zero frequency get length 0. The result is deterministic: among
// equal-frequency symbols, lower symbol values receive shorter-or-equal
// codes (byte-order tie-break).
//
// A single used symbol is assigned a 1-bit code so it never produces an
// undecodable empty codeword. An all-zero frequency slice yields all-zero
// lengths and no error.
func CodeLengths(freqs []uint64, maxBits int) ([]uint8, error) {
	lengths := make([]uint8, len(freqs))
	if maxBits < 1 || maxBits > MaxCodeLength {
		return nil, ErrMaxBitsTooSmall
	}
	var active []symWeight
	maxSym := -1
	for s, f := range freqs {
		if f > 0 {
			active = append(active, symWeight{sym: s, w: f})
			if s > maxSym {
				maxSym = s
			}
		}
	}
	switch len(active) {
	case 0:
		return lengths, nil
	case 1:
		lengths[active[0].sym] = 1
		return lengths, nil
	}
	if uint64(len(active)) > 1<<uint(maxBits) {
		return nil, ErrMaxBitsTooSmall
	}

	multiset := packageMerge(active, maxBits, maxSym+1)
	sort.Slice(multiset, func(i, j int) bool { return multiset[i] < multiset[j] })
	// Reassign the optimal length multiset deterministically: heavier
	// symbols first; equal weights broken by byte (symbol) order.
	sort.Slice(active, func(i, j int) bool {
		if active[i].w != active[j].w {
			return active[i].w > active[j].w
		}
		return active[i].sym < active[j].sym
	})
	for i, sw := range active {
		lengths[sw.sym] = multiset[i]
	}
	return lengths, nil
}

// pmItem is a package-merge node: a leaf or a merged package of leaves.
type pmItem struct {
	weight uint64
	minSym int
	syms   []int
}

func pmLess(a, b pmItem) bool {
	if a.weight != b.weight {
		return a.weight < b.weight
	}
	if a.minSym != b.minSym {
		return a.minSym < b.minSym
	}
	return len(a.syms) < len(b.syms)
}

type symWeight struct {
	sym int
	w   uint64
}

// packageMerge returns the multiset of optimal code lengths (one entry per
// active symbol) bounded by maxBits.
func packageMerge(active []symWeight, maxBits, numSlots int) []uint8 {
	n := len(active)
	leaves := make([]pmItem, n)
	for i, sw := range active {
		leaves[i] = pmItem{weight: sw.w, minSym: sw.sym, syms: []int{sw.sym}}
	}
	sort.Slice(leaves, func(i, j int) bool { return pmLess(leaves[i], leaves[j]) })

	merge := func(a, b []pmItem) []pmItem {
		out := make([]pmItem, 0, len(a)+len(b))
		i, j := 0, 0
		for i < len(a) && j < len(b) {
			if pmLess(a[i], b[j]) {
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

	prev := leaves
	for level := maxBits; level > 1; level-- {
		pkgs := make([]pmItem, 0, len(prev)/2)
		for i := 0; i+1 < len(prev); i += 2 {
			syms := make([]int, 0, len(prev[i].syms)+len(prev[i+1].syms))
			syms = append(syms, prev[i].syms...)
			syms = append(syms, prev[i+1].syms...)
			min := prev[i].minSym
			if prev[i+1].minSym < min {
				min = prev[i+1].minSym
			}
			pkgs = append(pkgs, pmItem{weight: prev[i].weight + prev[i+1].weight, minSym: min, syms: syms})
		}
		prev = merge(leaves, pkgs)
	}

	counts := make([]uint8, numSlots)
	for _, it := range prev[:2*n-2] {
		for _, s := range it.syms {
			counts[s]++
		}
	}
	multiset := make([]uint8, 0, n)
	for _, sw := range active {
		multiset = append(multiset, counts[sw.sym])
	}
	return multiset
}
