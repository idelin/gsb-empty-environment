// Package dagpath finds the best s-t path in a DAG whose edges carry string
// fragments. "Best" means: lexicographically smallest concatenated string,
// then fewest edges, then lexicographically smallest edge-ID sequence.
//
// This file implements an immutable concat rope so that candidate strings
// share suffix structure instead of being re-concatenated, and so that two
// candidates can be compared without materializing the full text.
package dagpath

// hash is a dual 64-bit polynomial rolling hash. Using two independent bases
// makes the chance of a false equality (which could corrupt a comparison)
// negligible without any big-integer arithmetic.
type hash struct {
	a uint64
	b uint64
}

const (
	baseA uint64 = 0x9e3779b97f4a7c15
	baseB uint64 = 0xc2b2ae3d27d4eb4f
)

// pow returns base^k for both bases (mod 2^64 by natural overflow).
func pow(k int) hash {
	ra, rb := uint64(1), uint64(1)
	ba, bb := baseA, baseB
	for k > 0 {
		if k&1 == 1 {
			ra *= ba
			rb *= bb
		}
		ba *= ba
		bb *= bb
		k >>= 1
	}
	return hash{a: ra, b: rb}
}

// combine returns the hash of a string whose left part has hash h1 and whose
// right part has length len2 and hash h2: hash(s1+s2) = h1*base^len2 + h2.
func combine(h1 hash, len2 int, h2 hash) hash {
	p := pow(len2)
	return hash{a: h1.a*p.a + h2.a, b: h1.b*p.b + h2.b}
}

// rope is an immutable string tree: either a leaf holding a literal fragment
// or a concat of two children. Suffixes are shared between candidates, so a
// path string is never copied; only O(edges-on-path) new nodes are allocated
// per candidate.
type rope struct {
	length int
	h      hash

	// Leaf data (left == nil). preA/preB[i] is the hash of str[:i], so a
	// prefix hash inside a leaf costs O(1).
	str  string
	preA []uint64
	preB []uint64

	// Concat data (left != nil).
	left  *rope
	right *rope
}

// emptyRope is the canonical empty string node.
var emptyRope = &rope{str: ""}

// leafRope wraps a literal fragment. Empty fragments are legal and map to the
// shared empty node so they never add depth to the tree.
func leafRope(s string) *rope {
	if s == "" {
		return emptyRope
	}
	r := &rope{
		length: len(s),
		str:    s,
		preA:   make([]uint64, len(s)+1),
		preB:   make([]uint64, len(s)+1),
	}
	for i := 0; i < len(s); i++ {
		r.preA[i+1] = r.preA[i]*baseA + uint64(s[i])
		r.preB[i+1] = r.preB[i]*baseB + uint64(s[i])
	}
	r.h = hash{a: r.preA[len(s)], b: r.preB[len(s)]}
	return r
}

// concatRope returns l followed by r, sharing both without copying.
func concatRope(l, r *rope) *rope {
	if l == nil || l.length == 0 {
		if r == nil {
			return emptyRope
		}
		return r
	}
	if r == nil || r.length == 0 {
		return l
	}
	return &rope{
		length: l.length + r.length,
		h:      combine(l.h, r.length, r.h),
		left:   l,
		right:  r,
	}
}

// prefixHash returns the hash of the first k characters of r.
func prefixHash(r *rope, k int) hash {
	if k <= 0 {
		return hash{}
	}
	if k >= r.length {
		return r.h
	}
	if r.left == nil {
		return hash{a: r.preA[k], b: r.preB[k]}
	}
	if k <= r.left.length {
		return prefixHash(r.left, k)
	}
	return combine(r.left.h, k-r.left.length, prefixHash(r.right, k-r.left.length))
}

// charAt returns the i-th byte of r.
func charAt(r *rope, i int) byte {
	for r.left != nil {
		if i < r.left.length {
			r = r.left
		} else {
			i -= r.left.length
			r = r.right
		}
	}
	return r.str[i]
}

// compareRope lexicographically compares two ropes without flattening either
// one: it binary-searches the longest common prefix with rolling hashes, then
// inspects the single differing byte. A string that is a strict prefix of
// another compares smaller.
func compareRope(a, b *rope) int {
	n := a.length
	if b.length < n {
		n = b.length
	}
	lo, hi := 0, n // invariant: prefixes of length lo match
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if prefixHash(a, mid) == prefixHash(b, mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == n {
		switch {
		case a.length < b.length:
			return -1
		case a.length > b.length:
			return 1
		default:
			return 0
		}
	}
	ca, cb := charAt(a, lo), charAt(b, lo)
	switch {
	case ca < cb:
		return -1
	case ca > cb:
		return 1
	default:
		return 0
	}
}

// ropeString materializes the rope; used only when expanding a final answer.
func ropeString(r *rope) string {
	if r == nil {
		return ""
	}
	buf := make([]byte, 0, r.length)
	var walk func(n *rope)
	walk = func(n *rope) {
		if n.left == nil {
			buf = append(buf, n.str...)
			return
		}
		walk(n.left)
		walk(n.right)
	}
	walk(r)
	return string(buf)
}
