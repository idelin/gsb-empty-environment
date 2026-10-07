// Package pseq implements an immutable persistent integer sequence with
// O(log n) push/pop at both ends, snapshots, concatenation and interval
// slicing. Every version carries a polynomial rolling digest under a fixed
// modulus together with its length. Negative elements are normalized into
// [0, mod) before hashing.
//
// A digest is only a summary: equal digests never imply equal content.
// Content equality is decided structurally by Equal.
package pseq

import "fmt"

// DefaultMod is used when NewFactory is not given an explicit modulus.
const DefaultMod = 1000000007

// hashBase is the polynomial base; it is reduced modulo mod before use.
const hashBase = 1000003

// maxMod keeps residue products inside uint64: (maxMod-1)^2 < 2^64.
const maxMod = 1<<32 - 1

type params struct {
	mod  uint64
	base uint64   // hashBase % mod
	pows []uint64 // pows[i] = base^i mod mod, grown lazily
}

func newParams(mod uint64) *params {
	if mod < 2 || mod > maxMod {
		panic(fmt.Sprintf("pseq: modulus %d out of range [2, %d]", mod, uint64(maxMod)))
	}
	return &params{mod: mod, base: hashBase % mod, pows: []uint64{1}}
}

func (p *params) pow(n int) uint64 {
	for len(p.pows) <= n {
		p.pows = append(p.pows, p.pows[len(p.pows)-1]*p.base%p.mod)
	}
	return p.pows[n]
}

// norm maps v into [0, mod); negative values are reduced first.
func (p *params) norm(v int64) uint64 {
	m := int64(p.mod)
	r := v % m
	if r < 0 {
		r += m
	}
	return uint64(r)
}

// combine folds a right segment of length lenR and digest hr onto hl.
func (p *params) combine(hl, hr uint64, lenR int) uint64 {
	return (hl*p.pow(lenR) + hr) % p.mod
}

// node is an immutable AVL rope node. Values live in leaves only; internal
// nodes cache height, size and the polynomial digest of their subtree.
type node struct {
	left, right *node
	height      int
	size        int
	hash        uint64
	val         int64 // meaningful only for leaves
}

func isLeaf(n *node) bool { return n.left == nil && n.right == nil }

func height(n *node) int {
	if n == nil {
		return 0
	}
	return n.height
}

func size(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

// Factory creates sequences sharing one modulus and hash parameters.
// Sequences from different factories must not be concatenated.
type Factory struct {
	p *params
}

// NewFactory returns a Factory for the given modulus (2 <= mod <= 2^32-1).
func NewFactory(mod uint64) *Factory {
	return &Factory{p: newParams(mod)}
}

// Mod returns the modulus every digest of this factory is computed under.
func (f *Factory) Mod() uint64 { return f.p.mod }

func (f *Factory) leaf(v int64) *node {
	return &node{height: 1, size: 1, hash: f.p.norm(v), val: v}
}

// mk builds an internal node, recomputing all cached aggregates.
func (f *Factory) mk(l, r *node) *node {
	h := height(l)
	if height(r) > h {
		h = height(r)
	}
	return &node{
		left:   l,
		right:  r,
		height: h + 1,
		size:   size(l) + size(r),
		hash:   f.p.combine(hashOf(l), hashOf(r), size(r)),
	}
}

func hashOf(n *node) uint64 {
	if n == nil {
		return 0
	}
	return n.hash
}

func (f *Factory) rotR(t *node) *node {
	l := t.left
	return f.mk(l.left, f.mk(l.right, t.right))
}

func (f *Factory) rotL(t *node) *node {
	r := t.right
	return f.mk(f.mk(t.left, r.left), r.right)
}

// balance restores the AVL invariant on a freshly built node whose
// subtrees are already balanced and differ in height by at most 2.
func (f *Factory) balance(t *node) *node {
	if height(t.left) > height(t.right)+1 {
		if height(t.left.left) < height(t.left.right) {
			return f.rotR(f.mk(f.rotL(t.left), t.right))
		}
		return f.rotR(t)
	}
	if height(t.right) > height(t.left)+1 {
		if height(t.right.right) < height(t.right.left) {
			return f.rotL(f.mk(t.left, f.rotR(t.right)))
		}
		return f.rotL(t)
	}
	return t
}

// join concatenates two trees; all elements of l precede those of r.
func (f *Factory) join(l, r *node) *node {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.height > r.height+1 {
		return f.balance(f.mk(l.left, f.join(l.right, r)))
	}
	if r.height > l.height+1 {
		return f.balance(f.mk(f.join(l, r.left), r.right))
	}
	return f.mk(l, r)
}

// split divides t into the first k elements and the rest.
func (f *Factory) split(t *node, k int) (*node, *node) {
	if t == nil {
		return nil, nil
	}
	if isLeaf(t) {
		if k == 0 {
			return nil, t
		}
		return t, nil
	}
	ls := size(t.left)
	switch {
	case k < ls:
		a, b := f.split(t.left, k)
		return a, f.join(b, t.right)
	case k > ls:
		a, b := f.split(t.right, k-ls)
		return f.join(t.left, a), b
	default:
		return t.left, t.right
	}
}

// Seq is an immutable persistent sequence. Every method returns a new
// version; the receiver is never modified, so versions may freely share
// underlying nodes without any mutable aliasing.
type Seq struct {
	f    *Factory
	root *node
}

// Empty returns the empty sequence.
func (f *Factory) Empty() *Seq { return &Seq{f: f} }

// Of builds a balanced sequence from vals.
func (f *Factory) Of(vals ...int64) *Seq {
	return &Seq{f: f, root: f.build(vals)}
}

func (f *Factory) build(vals []int64) *node {
	if len(vals) == 0 {
		return nil
	}
	if len(vals) == 1 {
		return f.leaf(vals[0])
	}
	m := len(vals) / 2
	return f.mk(f.build(vals[:m]), f.build(vals[m:]))
}

// Len returns the number of elements.
func (s *Seq) Len() int { return size(s.root) }

// Digest returns the polynomial digest of the whole sequence under the
// factory modulus. Equal digests do not imply equal content; use Equal.
func (s *Seq) Digest() uint64 { return hashOf(s.root) }

// PushBack returns a new version with v appended at the back.
func (s *Seq) PushBack(v int64) *Seq {
	return &Seq{f: s.f, root: s.f.join(s.root, s.f.leaf(v))}
}

// PushFront returns a new version with v prepended at the front.
func (s *Seq) PushFront(v int64) *Seq {
	return &Seq{f: s.f, root: s.f.join(s.f.leaf(v), s.root)}
}

// PopBack returns a new version without the last element, that element,
// and whether the sequence was non-empty.
func (s *Seq) PopBack() (*Seq, int64, bool) {
	if s.root == nil {
		return s, 0, false
	}
	l, r := s.f.split(s.root, s.Len()-1)
	return &Seq{f: s.f, root: l}, r.val, true
}

// PopFront returns a new version without the first element, that element,
// and whether the sequence was non-empty.
func (s *Seq) PopFront() (*Seq, int64, bool) {
	if s.root == nil {
		return s, 0, false
	}
	l, r := s.f.split(s.root, 1)
	return &Seq{f: s.f, root: r}, l.val, true
}

// Concat returns a new version with o appended after s. Both sequences
// must come from the same factory. Sharing of immutable nodes between the
// inputs and the result is structural and safe: no node is ever mutated.
func (s *Seq) Concat(o *Seq) *Seq {
	if s.f != o.f {
		panic("pseq: Concat of sequences from different factories")
	}
	return &Seq{f: s.f, root: s.f.join(s.root, o.root)}
}

// Slice returns the half-open interval [lo, hi) as a new version.
func (s *Seq) Slice(lo, hi int) *Seq {
	n := s.Len()
	if lo < 0 || hi < lo || hi > n {
		panic(fmt.Sprintf("pseq: Slice(%d, %d) out of range [0, %d]", lo, hi, n))
	}
	_, rest := s.f.split(s.root, lo)
	mid, _ := s.f.split(rest, hi-lo)
	return &Seq{f: s.f, root: mid}
}

// Evidence returns the digest and length of the half-open interval
// [lo, hi), recomputed from the persistent structure.
func (s *Seq) Evidence(lo, hi int) (digest uint64, length int) {
	sub := s.Slice(lo, hi)
	return sub.Digest(), sub.Len()
}

// At returns the i-th element.
func (s *Seq) At(i int) int64 {
	if i < 0 || i >= s.Len() {
		panic(fmt.Sprintf("pseq: At(%d) out of range [0, %d)", i, s.Len()))
	}
	n := s.root
	for !isLeaf(n) {
		if i < size(n.left) {
			n = n.left
		} else {
			i -= size(n.left)
			n = n.right
		}
	}
	return n.val
}

// Values returns all elements in order.
func (s *Seq) Values() []int64 {
	out := make([]int64, 0, s.Len())
	var stack []*node
	for n := s.root; n != nil || len(stack) > 0; {
		for n != nil {
			stack = append(stack, n)
			n = n.left
		}
		n = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if isLeaf(n) {
			out = append(out, n.val)
		}
		n = n.right
	}
	return out
}

// Equal reports whether both sequences hold the same raw elements in the
// same order. It never consults digests, so modular collisions between
// different contents are correctly reported as unequal.
func (s *Seq) Equal(o *Seq) bool {
	if s.Len() != o.Len() {
		return false
	}
	sv, ov := s.Values(), o.Values()
	for i := range sv {
		if sv[i] != ov[i] {
			return false
		}
	}
	return true
}

// Validate checks the AVL balance invariant and every cached size and
// digest against a fresh recomputation. It is a no-op on valid trees.
func (s *Seq) Validate() error {
	_, err := s.validate(s.root)
	return err
}

func (s *Seq) validate(n *node) (int, error) {
	if n == nil {
		return 0, nil
	}
	if isLeaf(n) {
		if n.height != 1 || n.size != 1 {
			return 0, fmt.Errorf("pseq: corrupt leaf: height=%d size=%d", n.height, n.size)
		}
		if want := s.f.p.norm(n.val); n.hash != want {
			return 0, fmt.Errorf("pseq: leaf digest %d, want %d", n.hash, want)
		}
		return 1, nil
	}
	hl, err := s.validate(n.left)
	if err != nil {
		return 0, err
	}
	hr, err := s.validate(n.right)
	if err != nil {
		return 0, err
	}
	if d := hl - hr; d < -1 || d > 1 {
		return 0, fmt.Errorf("pseq: unbalanced node: left height %d, right height %d", hl, hr)
	}
	if n.height != maxInt(hl, hr)+1 {
		return 0, fmt.Errorf("pseq: bad height %d", n.height)
	}
	if n.size != size(n.left)+size(n.right) {
		return 0, fmt.Errorf("pseq: bad size %d", n.size)
	}
	if want := s.f.p.combine(hashOf(n.left), hashOf(n.right), size(n.right)); n.hash != want {
		return 0, fmt.Errorf("pseq: bad digest %d, want %d", n.hash, want)
	}
	return n.height, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
