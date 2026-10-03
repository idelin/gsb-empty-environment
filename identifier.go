// Package lseq implements the LSEQ CRDT: an efficient collaborative
// sequence based on an unbounded-score causal tree with adaptive,
// variable-base identifier space allocation.
package lseq

import (
	"math"
	"math/rand"
)

const (
	// initialBaseBits sets the arity of the identifier tree at depth 0
	// to 2^3 = 8. The arity doubles at every deeper level (16, 32, ...),
	// which is the adaptive variable-base scheme of LSEQ: the more the
	// algorithm is forced to descend, the more room it allocates.
	initialBaseBits = 3
	// boundary is the maximum random step taken from the lower (boundary+)
	// or upper (boundary-) neighbour when picking a fresh digit. It keeps
	// identifiers dense and prevents bit-width explosion when a user types
	// continuously at the same spot.
	boundary = 10
)

// Digit is one component of an identifier path. Val is the position in the
// variable-base tree level, Site disambiguates concurrent allocations of the
// same Val and guarantees a total order.
type Digit struct {
	Val  uint64
	Site uint64
}

// Identifier is a path of digits from the root of the causal tree to the
// element. The zero (nil) Identifier is the virtual bound used at the
// beginning/end of the sequence.
type Identifier []Digit

// Compare returns -1, 0 or 1 depending on whether a orders before, equal to,
// or after b. Identifiers are totally ordered, so the merge of any set of
// operations is deterministic on every replica.
func Compare(a, b Identifier) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].Val != b[i].Val {
			if a[i].Val < b[i].Val {
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

// baseAt returns the arity of the tree at the given depth: 8, 16, 32, ...
// The shift is capped defensively; with the adaptive allocation below the
// depth stays logarithmic in the edit count and never comes close.
func baseAt(depth int) uint64 {
	if depth >= 61 {
		return math.MaxUint64
	}
	return uint64(1) << (initialBaseBits + uint(depth))
}

// Allocate generates a fresh identifier strictly between p and q (p < q is
// required; a nil identifier denotes a virtual sequence bound). It walks
// down the tree until it finds a level with free space, then picks a random
// digit within `boundary` of one of the two neighbours.
//
// The boundary strategy is direction-adaptive, which is what keeps the
// identifier depth logarithmic instead of linear under dense typing:
//   - at the right edge (q is the virtual bound) boundary+ hugs the left
//     neighbour, leaving the rest of the level free above;
//   - at the left edge (p is the virtual bound) boundary- hugs the right
//     neighbour, leaving the level free below;
//   - between two real neighbours the strategy is chosen at random.
//
// Consecutive inserts at one edge then perform a random walk away from the
// hugged neighbour, so a level of arity 2^(3+d) absorbs O(2^(3+d)/boundary)
// edits before the allocator has to descend. The strategy only affects
// identifier sizes; any choice strictly between p and q preserves the total
// order, so replicas converge regardless of the strategies they use.
func Allocate(p, q Identifier, site uint64, rng *rand.Rand) Identifier {
	// A nil identifier is a virtual bound: nil p is -inf, nil q is +inf,
	// so p == q == nil (the empty document) is a valid call.
	if p != nil && q != nil && Compare(p, q) >= 0 {
		panic("lseq: Allocate requires p < q")
	}
	var prefix []Digit
	for depth := 0; ; depth++ {
		lo := Digit{Val: 0, Site: site}
		if depth < len(p) {
			lo = p[depth]
		}
		hi := Digit{Val: baseAt(depth), Site: site}
		if depth < len(q) {
			hi = q[depth]
		}

		if hi.Val > lo.Val+1 {
			interval := hi.Val - lo.Val - 1
			step := uint64(boundary)
			if interval < step {
				step = interval
			}
			offset := uint64(rng.Int63n(int64(step))) + 1
			var val uint64
			switch {
			case q == nil:
				val = lo.Val + offset // right edge: boundary+
			case p == nil:
				val = hi.Val - offset // left edge: boundary-
			default:
				if rng.Intn(2) == 0 {
					val = lo.Val + offset // boundary+
				} else {
					val = hi.Val - offset // boundary-
				}
			}
			id := make(Identifier, len(prefix)+1)
			copy(id, prefix)
			id[len(prefix)] = Digit{Val: val, Site: site}
			return id
		}

		// No room at this level: descend, extending the prefix.
		if depth < len(p) {
			// Reuse p's digit so the new identifier stays greater than p.
			prefix = append(prefix, p[depth])
			continue
		}
		// p is exhausted. The fresh digit must stay below q's digit.
		// The last digit of any allocated identifier has Val >= 1, so a
		// zero-valued q digit is never q's last one and copying it is safe
		// and terminates.
		if hi.Val == 0 && site >= hi.Site {
			prefix = append(prefix, hi)
		} else {
			prefix = append(prefix, Digit{Val: 0, Site: site})
		}
	}
}
