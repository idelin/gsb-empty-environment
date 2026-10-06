// Package windowagg implements an aggregation window for monitoring data.
//
// A Window is a multiset of int64 values: values can be inserted and later
// revoked, and duplicates are counted individually. It reports the exact
// median and the mean after trimming the k smallest and k largest values.
// Means are returned as reduced rational numbers so no precision is lost,
// and sums are accumulated with big integers so large values cannot
// overflow int64.
package windowagg

import (
	"math/big"
	"math/rand"
)

// Window is a multiset of monitoring values. The zero value is ready to
// use. A Window is not safe for concurrent use.
type Window struct {
	root *node
	n    int
	rnd  *rand.Rand
}

// node is a treap node keyed by value. Subtree sizes and sums make
// rank/select and prefix-sum queries logarithmic.
type node struct {
	val      int64
	count    int // copies of val stored at this node
	priority uint64
	left     *node
	right    *node
	size     int      // total values in the subtree, counting duplicates
	sum      *big.Int // total sum of values in the subtree
}

// Insert adds one copy of v to the window.
func (w *Window) Insert(v int64) {
	if w.rnd == nil {
		w.rnd = rand.New(rand.NewSource(rand.Int63()))
	}
	w.root = insert(w.root, v, w.rnd.Uint64())
	w.n++
}

// Revoke removes one copy of v from the window. It reports whether v was
// present; the window is unchanged when it was not.
func (w *Window) Revoke(v int64) bool {
	var ok bool
	w.root, ok = revoke(w.root, v)
	if ok {
		w.n--
	}
	return ok
}

// Len returns the number of values in the window, counting duplicates.
func (w *Window) Len() int { return w.n }

// Sum returns the exact sum of all values in the window.
func (w *Window) Sum() *big.Int {
	if w.root == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(w.root.sum)
}

// Median returns the exact median of the window. For an even number of
// values it is the mean of the two middle values, as a reduced fraction.
// The second result is false when the window is empty.
func (w *Window) Median() (*big.Rat, bool) {
	if w.n == 0 {
		return nil, false
	}
	if w.n%2 == 1 {
		return new(big.Rat).SetInt64(selectVal(w.root, w.n/2)), true
	}
	sum := big.NewInt(selectVal(w.root, w.n/2-1))
	sum.Add(sum, big.NewInt(selectVal(w.root, w.n/2)))
	return new(big.Rat).SetFrac(sum, big.NewInt(2)), true
}

// TrimmedMean returns the exact mean of the window after discarding the k
// smallest and the k largest values, as a reduced fraction. The second
// result is false when k is negative or the window holds fewer than 2k+1
// values.
func (w *Window) TrimmedMean(k int) (*big.Rat, bool) {
	if k < 0 || w.n < 2*k+1 {
		return nil, false
	}
	kept := prefixSum(w.root, w.n-k) // sum of the n-k smallest values
	kept.Sub(kept, prefixSum(w.root, k))
	return new(big.Rat).SetFrac(kept, big.NewInt(int64(w.n-2*k))), true
}

func size(n *node) int {
	if n == nil {
		return 0
	}
	return n.size
}

func subSum(n *node) *big.Int {
	if n == nil {
		return new(big.Int)
	}
	return n.sum
}

func recalc(n *node) {
	n.size = n.count + size(n.left) + size(n.right)
	s := new(big.Int).Add(subSum(n.left), subSum(n.right))
	s.Add(s, big.NewInt(0).Mul(big.NewInt(n.val), big.NewInt(int64(n.count))))
	n.sum = s
}

func insert(n *node, v int64, prio uint64) *node {
	if n == nil {
		return &node{val: v, count: 1, priority: prio, size: 1, sum: big.NewInt(v)}
	}
	switch {
	case v == n.val:
		n.count++
	case v < n.val:
		n.left = insert(n.left, v, prio)
		if n.left.priority > n.priority {
			n = rotateRight(n)
		}
	default:
		n.right = insert(n.right, v, prio)
		if n.right.priority > n.priority {
			n = rotateLeft(n)
		}
	}
	recalc(n)
	return n
}

func revoke(n *node, v int64) (*node, bool) {
	if n == nil {
		return nil, false
	}
	if v == n.val {
		n.count--
		if n.count == 0 {
			return merge(n.left, n.right), true
		}
		recalc(n)
		return n, true
	}
	var ok bool
	if v < n.val {
		n.left, ok = revoke(n.left, v)
	} else {
		n.right, ok = revoke(n.right, v)
	}
	if ok {
		recalc(n)
	}
	return n, ok
}

// merge joins two treaps whose key ranges do not overlap.
func merge(l, r *node) *node {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.priority > r.priority {
		l.right = merge(l.right, r)
		recalc(l)
		return l
	}
	r.left = merge(l, r.left)
	recalc(r)
	return r
}

func rotateRight(n *node) *node {
	l := n.left
	n.left = l.right
	l.right = n
	recalc(n)
	recalc(l)
	return l
}

func rotateLeft(n *node) *node {
	r := n.right
	n.right = r.left
	r.left = n
	recalc(n)
	recalc(r)
	return r
}

// selectVal returns the value at rank r (0-based) in the subtree.
func selectVal(n *node, r int) int64 {
	for {
		ls := size(n.left)
		switch {
		case r < ls:
			n = n.left
		case r < ls+n.count:
			return n.val
		default:
			r -= ls + n.count
			n = n.right
		}
	}
}

// prefixSum returns the sum of the r smallest values in the subtree,
// for 0 <= r <= size(n).
func prefixSum(n *node, r int) *big.Int {
	switch {
	case r <= 0:
		return new(big.Int)
	case r >= n.size:
		return new(big.Int).Set(n.sum)
	}
	ls := size(n.left)
	switch {
	case r <= ls:
		return prefixSum(n.left, r)
	case r <= ls+n.count:
		take := big.NewInt(0).Mul(big.NewInt(n.val), big.NewInt(int64(r-ls)))
		return new(big.Int).Add(subSum(n.left), take)
	default:
		s := new(big.Int).Set(subSum(n.left))
		s.Add(s, big.NewInt(0).Mul(big.NewInt(n.val), big.NewInt(int64(n.count))))
		return s.Add(s, prefixSum(n.right, r-ls-n.count))
	}
}
