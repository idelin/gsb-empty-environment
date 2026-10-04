// Package segtree implements a fully persistent segment tree with lazy
// propagation over the ring Z/modZ (the modulus is supplied by the caller
// and does not need to be prime).
//
// Supported operations, each producing a new version in O(log n) time:
//
//	Assign: a[i] = v                for i in [l, r)
//	Affine: a[i] = mul*a[i] + add   for i in [l, r)
//	Sum:    a[l] + ... + a[r-1]     (mod mod)
//
// Lazy tags are affine transforms x -> mul*x + add and are composed in
// application order, which is NOT commutative (assign must override older
// transforms while newer transforms apply on top of an assign). Zero-length
// ranges (l == r) are legal: updates are no-ops that still create a new
// version, and sums over them are 0.
//
// All parameters are normalized into [0, mod), so negative values are fine.
// The modulus must satisfy 1 <= mod <= 3037000499 so that products of two
// residues never overflow int64.
package segtree

import "fmt"

// maxMod is floor(sqrt(2^63-1)); products of residues below it fit int64.
const maxMod = 3037000499

// node is one immutable-ish tree node in the shared node pool. Nodes are
// never mutated in place once published in a version; updates clone first.
type node struct {
	left, right int   // indices into Tree.nodes; 0 denotes an empty segment
	sum         int64 // segment sum mod mod, with the pending tag applied
	mul, add    int64 // pending lazy tag x -> mul*x + add; identity is (1, 0)
}

// Tree is a fully persistent segment tree. Version 0 is the all-zero array;
// every update appends a new version and returns its index.
type Tree struct {
	mod   int64
	n     int
	nodes []node
	roots []int
}

// New returns a tree over n positions (initially all zero) with modulus mod.
func New(n int, mod int64) *Tree {
	if n < 0 {
		panic("segtree: negative length")
	}
	if mod < 1 || mod > maxMod {
		panic(fmt.Sprintf("segtree: modulus %d out of range [1, %d]", mod, maxMod))
	}
	t := &Tree{mod: mod, n: n, nodes: make([]node, 1, 16)}
	t.nodes[0] = node{mul: 1} // canonical empty segment
	t.roots = append(t.roots, t.build(0, n))
	return t
}

// Size returns the number of positions.
func (t *Tree) Size() int { return t.n }

// Mod returns the modulus.
func (t *Tree) Mod() int64 { return t.mod }

// Versions returns the number of existing versions (indices 0..Versions()-1).
func (t *Tree) Versions() int { return len(t.roots) }

// norm maps v into [0, mod).
func (t *Tree) norm(v int64) int64 {
	v %= t.mod
	if v < 0 {
		v += t.mod
	}
	return v
}

func (t *Tree) build(l, r int) int {
	if l >= r {
		return 0
	}
	id := len(t.nodes)
	t.nodes = append(t.nodes, node{mul: 1})
	if l+1 < r {
		mid := l + (r-l)/2
		t.nodes[id].left = t.build(l, mid)
		t.nodes[id].right = t.build(mid, r)
	}
	return id
}

func (t *Tree) clone(id int) int {
	t.nodes = append(t.nodes, t.nodes[id])
	return len(t.nodes) - 1
}

// applyTag applies the transform x -> mul*x + add AFTER the tag already
// pending on id: combined x -> mul*(oldMul*x+oldAdd) + add.
func (t *Tree) applyTag(id, segLen int, mul, add int64) {
	nd := &t.nodes[id]
	nd.sum = (nd.sum*mul + add*int64(segLen)) % t.mod
	nd.add = (mul*nd.add + add) % t.mod
	nd.mul = nd.mul * mul % t.mod
}

// push propagates id's pending tag to freshly cloned children and resets it.
// id must be a private (newly cloned) node.
func (t *Tree) push(id, l, r int) {
	nd := t.nodes[id]
	if nd.mul == 1 && nd.add == 0 {
		return
	}
	if r-l > 1 {
		mid := l + (r-l)/2
		left := t.clone(nd.left)
		right := t.clone(nd.right)
		t.applyTag(left, mid-l, nd.mul, nd.add)
		t.applyTag(right, r-mid, nd.mul, nd.add)
		t.nodes[id].left = left
		t.nodes[id].right = right
	}
	t.nodes[id].mul = 1
	t.nodes[id].add = 0
}

func (t *Tree) update(root, l, r, ql, qr int, mul, add int64) int {
	if root == 0 || ql >= qr || ql >= r || qr <= l {
		return root
	}
	id := t.clone(root)
	if ql <= l && r <= qr {
		t.applyTag(id, r-l, mul, add)
		return id
	}
	t.push(id, l, r)
	mid := l + (r-l)/2
	t.nodes[id].left = t.update(t.nodes[id].left, l, mid, ql, qr, mul, add)
	t.nodes[id].right = t.update(t.nodes[id].right, mid, r, ql, qr, mul, add)
	t.nodes[id].sum = (t.nodes[t.nodes[id].left].sum + t.nodes[t.nodes[id].right].sum) % t.mod
	return id
}

// query sums [ql, qr) under segment [l, r). (am, aa) is the composition of
// pending tags of ancestors of root, to be applied on top of stored values:
// a node's own pending tag is older than any tag pending above it, so the
// accumulated transform is applied after the node's own tag.
func (t *Tree) query(root, l, r, ql, qr int, am, aa int64) int64 {
	if root == 0 || ql >= qr || ql >= r || qr <= l {
		return 0
	}
	nd := t.nodes[root]
	if ql <= l && r <= qr {
		return (am*nd.sum + aa*int64(r-l)) % t.mod
	}
	cm := am * nd.mul % t.mod
	ca := (am*nd.add + aa) % t.mod
	mid := l + (r-l)/2
	return (t.query(nd.left, l, mid, ql, qr, cm, ca) +
		t.query(nd.right, mid, r, ql, qr, cm, ca)) % t.mod
}

func (t *Tree) checkVersion(ver int) {
	if ver < 0 || ver >= len(t.roots) {
		panic(fmt.Sprintf("segtree: invalid version %d (have %d)", ver, len(t.roots)))
	}
}

func (t *Tree) checkRange(l, r int) {
	if l < 0 || r < l || r > t.n {
		panic(fmt.Sprintf("segtree: invalid range [%d, %d) for size %d", l, r, t.n))
	}
}

// Assign returns a new version derived from ver with a[i] = v for i in
// [l, r). v is normalized into [0, mod).
func (t *Tree) Assign(ver, l, r int, v int64) int {
	t.checkVersion(ver)
	t.checkRange(l, r)
	root := t.update(t.roots[ver], 0, t.n, l, r, 0, t.norm(v))
	t.roots = append(t.roots, root)
	return len(t.roots) - 1
}

// Affine returns a new version derived from ver with
// a[i] = mul*a[i] + add (mod mod) for i in [l, r). mul and add are
// normalized into [0, mod).
func (t *Tree) Affine(ver, l, r int, mul, add int64) int {
	t.checkVersion(ver)
	t.checkRange(l, r)
	root := t.update(t.roots[ver], 0, t.n, l, r, t.norm(mul), t.norm(add))
	t.roots = append(t.roots, root)
	return len(t.roots) - 1
}

// Sum returns a[l] + ... + a[r-1] (mod mod) in version ver. An empty range
// sums to 0.
func (t *Tree) Sum(ver, l, r int) int64 {
	t.checkVersion(ver)
	t.checkRange(l, r)
	return t.query(t.roots[ver], 0, t.n, l, r, 1, 0)
}
