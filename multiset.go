// Package wquantile provides an online weighted quantile structure over
// integer values with non-negative integer weights, plus a coordinator
// that merges quantile inputs from multiple disconnectable partitions
// behind barrier-consistent cuts.
package wquantile

import (
	"errors"
	"math/rand"
	"sort"
)

// ErrEmpty is returned when a quantile is requested from an empty set.
var ErrEmpty = errors.New("wquantile: empty set")

// ErrInsufficientWeight is returned when a removal asks for more weight
// than is present for a value.
var ErrInsufficientWeight = errors.New("wquantile: insufficient weight to remove")

// Entry is a single distinct value and its total weight.
type Entry struct {
	Value  int64
	Weight uint64
}

type node struct {
	value  int64
	weight uint64 // weight stored at this value
	total  uint64 // sum of weights in the subtree
	prio   uint64
	left   *node
	right  *node
}

func totalOf(n *node) uint64 {
	if n == nil {
		return 0
	}
	return n.total
}

func (n *node) recalc() {
	n.total = n.weight + totalOf(n.left) + totalOf(n.right)
}

func rotateRight(n *node) *node {
	l := n.left
	n.left = l.right
	l.right = n
	n.recalc()
	l.recalc()
	return l
}

func rotateLeft(n *node) *node {
	r := n.right
	n.right = r.left
	r.left = n
	n.recalc()
	r.recalc()
	return r
}

// Multiset is a multiset of int64 values with non-negative integer
// weights. Values whose weight drops to zero are removed automatically.
type Multiset struct {
	root  *node
	total uint64
	rng   *rand.Rand
}

// NewMultiset returns an empty Multiset.
func NewMultiset() *Multiset {
	return &Multiset{rng: rand.New(rand.NewSource(1))}
}

// Total returns the sum of all weights in the set.
func (m *Multiset) Total() uint64 { return m.total }

// Len returns the number of distinct values with positive weight.
func (m *Multiset) Len() int {
	n := 0
	var walk func(x *node)
	walk = func(x *node) {
		if x == nil {
			return
		}
		walk(x.left)
		n++
		walk(x.right)
	}
	walk(m.root)
	return n
}

// Add inserts weight w of value v. Adding zero weight is a no-op.
func (m *Multiset) Add(v int64, w uint64) {
	if w == 0 {
		return
	}
	m.root = m.insert(m.root, v, w)
	m.total += w
}

func (m *Multiset) insert(n *node, v int64, w uint64) *node {
	if n == nil {
		return &node{value: v, weight: w, total: w, prio: m.rng.Uint64()}
	}
	switch {
	case v < n.value:
		n.left = m.insert(n.left, v, w)
		if n.left.prio < n.prio {
			n = rotateRight(n)
		}
	case v > n.value:
		n.right = m.insert(n.right, v, w)
		if n.right.prio < n.prio {
			n = rotateLeft(n)
		}
	default:
		n.weight += w
	}
	n.recalc()
	return n
}

// Remove deletes weight w of value v. It returns ErrInsufficientWeight
// if the set holds less than w of v. Removing zero weight is a no-op.
func (m *Multiset) Remove(v int64, w uint64) error {
	if w == 0 {
		return nil
	}
	n := m.root
	for n != nil && n.value != v {
		if v < n.value {
			n = n.left
		} else {
			n = n.right
		}
	}
	if n == nil || n.weight < w {
		return ErrInsufficientWeight
	}
	m.root = m.remove(m.root, v, w)
	m.total -= w
	return nil
}

func (m *Multiset) remove(n *node, v int64, w uint64) *node {
	switch {
	case v < n.value:
		n.left = m.remove(n.left, v, w)
	case v > n.value:
		n.right = m.remove(n.right, v, w)
	default:
		n.weight -= w
		if n.weight == 0 {
			return mergeTreaps(n.left, n.right)
		}
	}
	n.recalc()
	return n
}

func mergeTreaps(l, r *node) *node {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio < r.prio {
		l.right = mergeTreaps(l.right, r)
		l.recalc()
		return l
	}
	r.left = mergeTreaps(l, r.left)
	r.recalc()
	return r
}

// Quantile returns the smallest value v such that the cumulative weight
// of all values <= v is at least q*Total. q is clamped to [0,1]; q=0
// yields the minimum value with positive weight and q=1 the maximum.
// It returns ErrEmpty when the set is empty.
func (m *Multiset) Quantile(q float64) (int64, error) {
	if m.total == 0 {
		return 0, ErrEmpty
	}
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	thr := q * float64(m.total)
	if thr <= 0 {
		// q=0: the minimum value with positive weight.
		n := m.root
		for n.left != nil {
			n = n.left
		}
		return n.value, nil
	}
	var acc uint64 // weight strictly left of the current path
	n := m.root
	for n != nil {
		left := totalOf(n.left)
		if float64(acc+left) >= thr {
			n = n.left
			continue
		}
		if float64(acc+left+n.weight) >= thr {
			return n.value, nil
		}
		acc += left + n.weight
		n = n.right
	}
	// Unreachable for q<=1, but stay safe against float rounding.
	return m.maxValue(), nil
}

func (m *Multiset) maxValue() int64 {
	n := m.root
	for n.right != nil {
		n = n.right
	}
	return n.value
}

// Entries returns all distinct values and their weights, sorted by value.
func (m *Multiset) Entries() []Entry {
	var out []Entry
	var walk func(x *node)
	walk = func(x *node) {
		if x == nil {
			return
		}
		walk(x.left)
		out = append(out, Entry{Value: x.value, Weight: x.weight})
		walk(x.right)
	}
	walk(m.root)
	return out
}

// LoadEntries replaces the contents with the given entries, which may be
// in any order. Zero-weight entries are dropped.
func (m *Multiset) LoadEntries(entries []Entry) {
	sorted := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Weight > 0 {
			sorted = append(sorted, e)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Value < sorted[j].Value })
	// Merge duplicate values.
	merged := sorted[:0]
	for _, e := range sorted {
		if len(merged) > 0 && merged[len(merged)-1].Value == e.Value {
			merged[len(merged)-1].Weight += e.Weight
		} else {
			merged = append(merged, e)
		}
	}
	nodes := make([]node, len(merged))
	for i, e := range merged {
		nodes[i] = node{value: e.Value, weight: e.Weight, prio: m.rng.Uint64()}
	}
	m.root = buildCartesian(nodes)
	m.total = totalOf(m.root)
}

// buildCartesian builds a treap from in-order sorted nodes in O(n).
func buildCartesian(nodes []node) *node {
	if len(nodes) == 0 {
		return nil
	}
	ptrs := make([]*node, len(nodes))
	for i := range nodes {
		ptrs[i] = &nodes[i]
	}
	var stack []*node
	for _, cur := range ptrs {
		var last *node
		for len(stack) > 0 && stack[len(stack)-1].prio > cur.prio {
			last = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
		}
		cur.left = last
		if len(stack) > 0 {
			stack[len(stack)-1].right = cur
		}
		stack = append(stack, cur)
	}
	root := stack[0]
	// Fix totals bottom-up.
	var fix func(n *node) uint64
	fix = func(n *node) uint64 {
		if n == nil {
			return 0
		}
		n.total = n.weight + fix(n.left) + fix(n.right)
		return n.total
	}
	fix(root)
	return root
}
