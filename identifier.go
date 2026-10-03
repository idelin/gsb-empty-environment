// Package lseq implements the LSEQ (Logoot-Split) replicated sequence:
// dense, unbounded fractional identifiers over a causal tree with an
// adaptive variable-base allocation strategy.
package lseq

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
)

const (
	// maxDigit is the largest digit a single position may hold.
	maxDigit = math.MaxUint32
	// baseBits controls the allocation boundary at depth 0 (2^baseBits).
	// The boundary doubles at every additional depth, so deeper levels of
	// the tree offer exponentially more room and depth grows slowly.
	baseBits = 4
)

// Position is one component of an Identifier: a digit inside the variable
// base of its depth, tie-broken by the site that allocated it.
type Position struct {
	Digit uint32
	Site  uint64
}

// Identifier is a path of positions from the root of the causal tree.
// The empty Identifier is the implicit lower bound (-inf); an identifier
// is always strictly smaller than any of its extensions.
type Identifier []Position

// Compare returns -1, 0 or 1 depending on whether a orders before, equal
// to, or after b. It defines the total order used by every replica, so it
// is the only ordering rule the whole system ever needs.
func Compare(a, b Identifier) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].Digit != b[i].Digit {
			if a[i].Digit < b[i].Digit {
				return -1
			}
			return 1
		}
		if a[i].Site != b[i].Site {
			if a[i].Site < b[i].Site {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// Depth is the number of positions; identifier bit-width grows with depth.
func (id Identifier) Depth() int { return len(id) }

func (id Identifier) String() string {
	parts := make([]string, len(id))
	for i, p := range id {
		parts[i] = fmt.Sprintf("%d#%x", p.Digit, p.Site)
	}
	return "(" + strings.Join(parts, ")(") + ")"
}

// digitAt returns the digit of id at depth, or def when id is shorter.
// Missing positions read as 0 on the left bound and maxDigit on the right
// bound, which is what makes the identifier space dense and unbounded.
func digitAt(id Identifier, depth int, def uint32) uint32 {
	if depth < len(id) {
		return id[depth].Digit
	}
	return def
}

// boundary is the allocation window at a given depth: 2^(baseBits+depth).
func boundary(depth int) uint64 {
	shift := uint(baseBits + depth)
	if shift >= 32 {
		return uint64(maxDigit)
	}
	return 1 << shift
}

// Allocator generates identifiers between two neighbours. Each replica
// owns one Allocator; the per-depth strategy (boundary+ / boundary-) is
// picked at random the first time a depth is used, as in the LSEQ paper,
// so no fixed insertion pattern can hit the worst case at every level.
type Allocator struct {
	site     uint64
	rng      *rand.Rand
	strategy []bool // strategy[d] == true -> boundary+ at depth d
}

// NewAllocator returns an allocator for the given site. rng may be nil,
// in which case a time-seeded source is used.
func NewAllocator(site uint64, rng *rand.Rand) *Allocator {
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(site)*2654435761 + 1))
	}
	return &Allocator{site: site, rng: rng}
}

func (a *Allocator) strategyAt(depth int) bool {
	for len(a.strategy) <= depth {
		a.strategy = append(a.strategy, a.rng.Intn(2) == 0)
	}
	return a.strategy[depth]
}

// Allocate returns a fresh identifier id with prev < id < next. Either
// bound may be nil, meaning -inf / +inf respectively.
func (a *Allocator) Allocate(prev, next Identifier) Identifier {
	depth := 0
	for {
		p := uint64(digitAt(prev, depth, 0))
		n := uint64(digitAt(next, depth, maxDigit))
		if n-p > 1 {
			b := boundary(depth)
			var lo, hi uint64
			if a.strategyAt(depth) {
				// boundary+: pack close to the left neighbour, leaving
				// room on the right for future rightward insertions.
				lo = p + 1
				hi = p + b
				if hi > n-1 {
					hi = n - 1
				}
			} else {
				// boundary-: pack close to the right neighbour.
				hi = n - 1
				if b >= n-p-1 {
					lo = p + 1
				} else {
					lo = n - b
				}
			}
			d := lo + uint64(a.rng.Int63n(int64(hi-lo+1)))
			id := make(Identifier, depth+1)
			// The prefix is copied from prev (full positions, sites
			// included); depths beyond len(prev) stay the implicit zero.
			npre := depth
			if len(prev) < npre {
				npre = len(prev)
			}
			copy(id, prev[:npre])
			id[depth] = Position{Digit: uint32(d), Site: a.site}
			return id
		}
		depth++
	}
}
