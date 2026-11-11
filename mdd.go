package netsec

import (
	"encoding/binary"
	"math/big"
	"slices"
)

// Terminal node ids of the decision diagram.
const (
	mddFalse = 0
	mddTrue  = 1
)

// mddNode is a reduced, ordered multi-valued decision diagram node.
// edges is complete: it holds exactly one child per domain value of the
// node's level, so boolean operations never have to reason about
// implicit edges.
type mddNode struct {
	level int
	edges map[int]int
}

type nodeKey struct {
	level int
	enc   string
}

// mdd is a canonical MDD manager: identical (level, edges) pairs share a
// single node, so union/intersection/difference run in time proportional
// to the diagram size rather than the expanded Cartesian product.
type mdd struct {
	numLevels int
	sizes     []int
	pref      []*big.Int // pref[i] = product of sizes[0:i]
	nodes     []mddNode
	unique    map[nodeKey]int
}

func newMDD(sizes []int) *mdd {
	pref := make([]*big.Int, len(sizes)+1)
	pref[0] = big.NewInt(1)
	for i, s := range sizes {
		pref[i+1] = new(big.Int).Mul(pref[i], big.NewInt(int64(s)))
	}
	m := &mdd{numLevels: len(sizes), sizes: sizes, pref: pref, unique: make(map[nodeKey]int)}
	m.nodes = append(m.nodes,
		mddNode{level: len(sizes)}, // false terminal
		mddNode{level: len(sizes)}, // true terminal
	)
	return m
}

func encodeEdges(edges map[int]int) string {
	vs := make([]int, 0, len(edges))
	for v := range edges {
		vs = append(vs, v)
	}
	slices.Sort(vs)
	buf := make([]byte, 0, len(vs)*4)
	var tmp [binary.MaxVarintLen64]byte
	for _, v := range vs {
		n := binary.PutUvarint(tmp[:], uint64(v))
		buf = append(buf, tmp[:n]...)
		n = binary.PutUvarint(tmp[:], uint64(edges[v]))
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}

// mk returns the canonical node for (level, edges), applying the MDD
// reduction rule: a node whose (complete) edges all lead to the same
// child collapses into that child.
func (m *mdd) mk(level int, edges map[int]int) int {
	first := -1
	same := true
	for _, c := range edges {
		if first < 0 {
			first = c
			continue
		}
		if c != first {
			same = false
			break
		}
	}
	if same {
		return first
	}
	key := nodeKey{level: level, enc: encodeEdges(edges)}
	if id, ok := m.unique[key]; ok {
		return id
	}
	id := len(m.nodes)
	m.nodes = append(m.nodes, mddNode{level: level, edges: edges})
	m.unique[key] = id
	return id
}

// buildSets constructs the diagram for a single pattern: the conjunction
// of per-level allowed value sets.
func (m *mdd) buildSets(sets []map[int]bool) int {
	next := mddTrue
	for lvl := len(sets) - 1; lvl >= 0; lvl-- {
		edges := make(map[int]int, m.sizes[lvl])
		for v := 0; v < m.sizes[lvl]; v++ {
			if sets[lvl][v] {
				edges[v] = next
			} else {
				edges[v] = mddFalse
			}
		}
		next = m.mk(lvl, edges)
	}
	return next
}

type applyOp func(a, b bool) bool

func opOr(a, b bool) bool     { return a || b }
func opAnd(a, b bool) bool    { return a && b }
func opAndNot(a, b bool) bool { return a && !b }

// apply combines two diagrams with a boolean operator using memoized
// recursion over the product of the two node graphs.
func (m *mdd) apply(op applyOp, a, b int) int {
	type pair struct{ a, b int }
	memo := make(map[pair]int)
	var rec func(a, b int) int
	rec = func(a, b int) int {
		aTerm := a <= mddTrue
		bTerm := b <= mddTrue
		if aTerm && bTerm {
			if op(a == mddTrue, b == mddTrue) {
				return mddTrue
			}
			return mddFalse
		}
		if aTerm {
			av := a == mddTrue
			f0, f1 := op(av, false), op(av, true)
			switch {
			case f0 == f1:
				if f0 {
					return mddTrue
				}
				return mddFalse
			case !f0 && f1: // identity in b
				return b
			}
		}
		if bTerm {
			bv := b == mddTrue
			f0, f1 := op(false, bv), op(true, bv)
			switch {
			case f0 == f1:
				if f0 {
					return mddTrue
				}
				return mddFalse
			case !f0 && f1: // identity in a
				return a
			}
		}
		key := pair{a, b}
		if v, ok := memo[key]; ok {
			return v
		}
		la, lb := m.nodes[a].level, m.nodes[b].level
		top := la
		if lb < top {
			top = lb
		}
		edges := make(map[int]int, m.sizes[top])
		for v := 0; v < m.sizes[top]; v++ {
			ca, cb := a, b
			if la == top {
				ca = m.nodes[a].edges[v]
			}
			if lb == top {
				cb = m.nodes[b].edges[v]
			}
			edges[v] = rec(ca, cb)
		}
		id := m.mk(top, edges)
		memo[key] = id
		return id
	}
	return rec(a, b)
}

// count returns the exact number of tuples accepted by the diagram.
// Levels collapsed by the reduction contribute their whole domain
// product, tracked through the prefix products of the domain sizes.
func (m *mdd) count(root int) *big.Int {
	memo := make([]*big.Int, len(m.nodes))
	var rec func(n int) *big.Int
	rec = func(n int) *big.Int {
		if n == mddFalse {
			return new(big.Int)
		}
		if n == mddTrue {
			return big.NewInt(1)
		}
		if memo[n] != nil {
			return memo[n]
		}
		l := m.nodes[n].level
		sum := new(big.Int)
		for _, c := range m.nodes[n].edges {
			cl := m.nodes[c].level
			factor := new(big.Int).Quo(m.pref[cl], m.pref[l+1])
			sum.Add(sum, factor.Mul(factor, rec(c)))
		}
		memo[n] = sum
		return sum
	}
	total := rec(root)
	head := new(big.Int).Quo(m.pref[m.nodes[root].level], m.pref[0])
	return total.Mul(total, head)
}

// contains reports whether the tuple of domain value indices is accepted.
func (m *mdd) contains(root int, vals []int) bool {
	n := root
	for n > mddTrue {
		c, ok := m.nodes[n].edges[vals[m.nodes[n].level]]
		if !ok {
			return false
		}
		n = c
	}
	return n == mddTrue
}

// minWitness returns the lexicographically smallest tuple (by domain
// order) accepted by the diagram, walking the diagram greedily without
// enumerating the accepted set.
func (m *mdd) minWitness(root int) ([]int, bool) {
	if root == mddFalse {
		return nil, false
	}
	// Children always have smaller ids than their parents because mk only
	// creates a node after its children exist, so a forward scan yields a
	// valid reachability computation.
	can := make([]bool, len(m.nodes))
	can[mddTrue] = true
	for i := 2; i < len(m.nodes); i++ {
		for _, c := range m.nodes[i].edges {
			if can[c] {
				can[i] = true
				break
			}
		}
	}
	if !can[root] {
		return nil, false
	}
	path := make([]int, 0, m.numLevels)
	n := root
	for n > mddTrue {
		// Levels skipped by the reduction accept every value; the
		// minimal choice is always domain value 0.
		for len(path) < m.nodes[n].level {
			path = append(path, 0)
		}
		best := -1
		for v, c := range m.nodes[n].edges {
			if can[c] && (best < 0 || v < best) {
				best = v
			}
		}
		path = append(path, best)
		n = m.nodes[n].edges[best]
	}
	for len(path) < m.numLevels {
		path = append(path, 0)
	}
	return path, true
}
