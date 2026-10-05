// Package pst implements a persistent (versioned) segment tree supporting
// range assign, range affine transformation (x -> x*mul + add), and range
// sum queries. All values are kept normalized modulo a runtime-specified
// modulus m (m >= 1, not necessarily prime).
//
// Lazy tags are affine maps composed in chronological order; composition is
// NOT commutative: assigning then multiplying differs from multiplying then
// assigning. Half-open intervals [l, r) are used everywhere, and zero-length
// intervals (l == r) are legal: updates are no-ops and sums are 0.
package pst

import (
	"fmt"
	"math/bits"
)

type node struct {
	left, right int32
	sum         int64
	mul, add    int64 // pending affine tag: x -> x*mul + add; identity is (1, 0)
}

// Tree is a persistent segment tree over positions [0, n) modulo m.
// Each update appends a new version and returns its index; old versions
// remain fully usable (structural sharing).
type Tree struct {
	n, m  int64
	nodes []node
	roots []int32
}

// NewTree returns a tree over n zero-valued positions modulo m.
func NewTree(n, m int64) *Tree {
	if n < 0 {
		panic(fmt.Sprintf("pst: negative size %d", n))
	}
	if m < 1 {
		panic(fmt.Sprintf("pst: modulus must be >= 1, got %d", m))
	}
	t := &Tree{n: n, m: m}
	// Node 0 is the canonical all-zero segment of any length.
	t.nodes = append(t.nodes, node{left: 0, right: 0, sum: 0, mul: 1, add: 0})
	t.roots = append(t.roots, 0)
	return t
}

// NewTreeFrom returns a tree whose version 0 holds the given values
// (normalized modulo m).
func NewTreeFrom(a []int64, m int64) *Tree {
	t := NewTree(int64(len(a)), m)
	if len(a) > 0 {
		t.roots[0] = t.build(a, 0, int64(len(a)))
	}
	return t
}

func (t *Tree) build(a []int64, l, r int64) int32 {
	if l+1 == r {
		return t.alloc(node{sum: norm(a[l], t.m), mul: 1})
	}
	mid := l + (r-l)/2
	left := t.build(a, l, mid)
	right := t.build(a, mid, r)
	return t.alloc(node{
		left:  left,
		right: right,
		sum:   addMod(t.nodes[left].sum, t.nodes[right].sum, t.m),
		mul:   1,
	})
}

// Versions returns the number of existing versions.
func (t *Tree) Versions() int { return len(t.roots) }

// Assign returns a new version where positions [l, r) are set to v (mod m).
// A zero-length interval is a no-op and returns ver itself.
func (t *Tree) Assign(ver int, l, r, v int64) int {
	return t.apply(ver, l, r, 0, v)
}

// Affine returns a new version where every x in [l, r) becomes x*mul + add
// (mod m). A zero-length interval is a no-op and returns ver itself.
func (t *Tree) Affine(ver int, l, r, mul, add int64) int {
	return t.apply(ver, l, r, mul, add)
}

// Sum returns the sum of positions [l, r) in version ver, modulo m.
// A zero-length interval sums to 0.
func (t *Tree) Sum(ver int, l, r int64) int64 {
	t.check(ver, l, r)
	if l == r {
		return 0
	}
	return t.query(t.roots[ver], 0, t.n, l, r, 1, 0)
}

func (t *Tree) apply(ver int, l, r, mul, add int64) int {
	t.check(ver, l, r)
	if l == r {
		return ver
	}
	root := t.update(t.roots[ver], 0, t.n, l, r, norm(mul, t.m), norm(add, t.m))
	t.roots = append(t.roots, root)
	return len(t.roots) - 1
}

func (t *Tree) check(ver int, l, r int64) {
	if ver < 0 || ver >= len(t.roots) {
		panic(fmt.Sprintf("pst: bad version %d (have %d)", ver, len(t.roots)))
	}
	if l < 0 || r > t.n || l > r {
		panic(fmt.Sprintf("pst: bad interval [%d, %d) for size %d", l, r, t.n))
	}
}

func (t *Tree) alloc(nd node) int32 {
	t.nodes = append(t.nodes, nd)
	return int32(len(t.nodes) - 1)
}

// compose returns the tag for "apply old first, then nxt": the composition
// order is significant and is not commutative.
func compose(oldMul, oldAdd, newMul, newAdd, m int64) (int64, int64) {
	return mulMod(oldMul, newMul, m), addMod(mulMod(oldAdd, newMul, m), newAdd, m)
}

// applyTag clones node i with the affine tag applied to its sum and composed
// onto its pending tag. length is the segment length of node i.
func (t *Tree) applyTag(i int32, length, mul, add int64) int32 {
	nd := t.nodes[i]
	nd.sum = addMod(mulMod(nd.sum, mul, t.m), mulMod(add, length%t.m, t.m), t.m)
	nd.mul, nd.add = compose(nd.mul, nd.add, mul, add, t.m)
	return t.alloc(nd)
}

// push clones node i with its pending tag pushed into (clones of) its
// children, leaving node i's own tag as identity.
func (t *Tree) push(i int32, l, r int64) int32 {
	nd := t.nodes[i]
	if nd.mul == 1 && nd.add == 0 {
		return i
	}
	mid := l + (r-l)/2
	left := t.applyTag(nd.left, mid-l, nd.mul, nd.add)
	right := t.applyTag(nd.right, r-mid, nd.mul, nd.add)
	return t.alloc(node{left: left, right: right, sum: nd.sum, mul: 1})
}

func (t *Tree) update(i int32, l, r, ql, qr, mul, add int64) int32 {
	if qr <= l || r <= ql {
		return i
	}
	if ql <= l && r <= qr {
		return t.applyTag(i, r-l, mul, add)
	}
	i = t.push(i, l, r)
	nd := t.nodes[i]
	mid := l + (r-l)/2
	left := t.update(nd.left, l, mid, ql, qr, mul, add)
	right := t.update(nd.right, mid, r, ql, qr, mul, add)
	return t.alloc(node{
		left:  left,
		right: right,
		sum:   addMod(t.nodes[left].sum, t.nodes[right].sum, t.m),
		mul:   1,
	})
}

// query accumulates pending tags from ancestors (am, aa) instead of pushing,
// so it never allocates. Ancestor tags apply after the node's own tag.
func (t *Tree) query(i int32, l, r, ql, qr, am, aa int64) int64 {
	if qr <= l || r <= ql {
		return 0
	}
	if ql <= l && r <= qr {
		return addMod(mulMod(t.nodes[i].sum, am, t.m), mulMod(aa, (r-l)%t.m, t.m), t.m)
	}
	nd := t.nodes[i]
	cm, ca := compose(nd.mul, nd.add, am, aa, t.m)
	mid := l + (r-l)/2
	return addMod(
		t.query(nd.left, l, mid, ql, qr, cm, ca),
		t.query(nd.right, mid, r, ql, qr, cm, ca),
		t.m,
	)
}

// norm maps x into [0, m), including for negative x.
func norm(x, m int64) int64 {
	x %= m
	if x < 0 {
		x += m
	}
	return x
}

// addMod computes (a + b) mod m for a, b in [0, m) without overflow.
func addMod(a, b, m int64) int64 {
	if a >= m-b {
		return a - (m - b)
	}
	return a + b
}

// mulMod computes (a * b) mod m for a, b in [0, m) via 128-bit arithmetic,
// safe for any m in [1, MaxInt64].
func mulMod(a, b, m int64) int64 {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	_, rem := bits.Div64(hi, lo, uint64(m))
	return int64(rem)
}
