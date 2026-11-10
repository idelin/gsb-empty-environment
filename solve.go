package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	refUnfilled = -2 // field not yet assigned during search
	refNull     = -1 // field explicitly null
)

// maxExplore caps the number of generated states as a safety valve.
const maxExplore = 2_000_000

// node is one object in a (possibly partial) object graph.
type node struct {
	typ  int
	refs []int // per declared field: refUnfilled, refNull, or an object index
}

type state struct {
	nodes []node
}

type solution struct {
	nodes  []node // complete graph, index 0 is the root
	shared []int  // objects reachable from both danger fields
}

func newNode(m *model, typ int) node {
	refs := make([]int, len(m.spec.Types[typ].Fields))
	for i := range refs {
		refs[i] = refUnfilled
	}
	return node{typ: typ, refs: refs}
}

func (s *state) key() string {
	var b strings.Builder
	for _, nd := range s.nodes {
		fmt.Fprintf(&b, "%d:", nd.typ)
		for _, r := range nd.refs {
			fmt.Fprintf(&b, "%d,", r)
		}
		b.WriteByte(';')
	}
	return b.String()
}

func (s *state) clone() *state {
	ns := &state{nodes: make([]node, len(s.nodes))}
	for i, nd := range s.nodes {
		refs := make([]int, len(nd.refs))
		copy(refs, nd.refs)
		ns.nodes[i] = node{typ: nd.typ, refs: refs}
	}
	return ns
}

// firstUnfilled returns the first unassigned field in scan order
// (node index, then declared field order), which makes expansion canonical.
func (s *state) firstUnfilled() (int, int, bool) {
	for ni, nd := range s.nodes {
		for fi, r := range nd.refs {
			if r == refUnfilled {
				return ni, fi, true
			}
		}
	}
	return 0, 0, false
}

// complete fills every remaining unassigned field with null.
func (s *state) complete() {
	for ni := range s.nodes {
		for fi := range s.nodes[ni].refs {
			if s.nodes[ni].refs[fi] == refUnfilled {
				s.nodes[ni].refs[fi] = refNull
			}
		}
	}
}

// reachSet returns the objects reachable from the root's field `field`,
// following only assigned, non-null references (cycles handled by `seen`).
func reachSet(m *model, nodes []node, field string) map[int]bool {
	seen := map[int]bool{}
	fi, ok := m.fieldIdx[nodes[0].typ][field]
	if !ok {
		return seen
	}
	start := nodes[0].refs[fi]
	if start < 0 {
		return seen
	}
	stack := []int{start}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[v] {
			continue
		}
		seen[v] = true
		for _, w := range nodes[v].refs {
			if w >= 0 && !seen[w] {
				stack = append(stack, w)
			}
		}
	}
	return seen
}

// sharedObjects returns the objects reachable from both danger fields,
// optionally restricted to trigger-bearing types.
func sharedObjects(m *model, nodes []node) []int {
	a := reachSet(m, nodes, m.spec.DangerFields[0])
	b := reachSet(m, nodes, m.spec.DangerFields[1])
	var shared []int
	for v := range a {
		if !b[v] {
			continue
		}
		if m.spec.TriggeredOnly && !m.hasTrig[nodes[v].typ] {
			continue
		}
		shared = append(shared, v)
	}
	sort.Ints(shared)
	return shared
}

// solve searches for a legal object graph with the fewest objects that
// satisfies the danger condition. It explores states grouped by object
// count (a 0-1 BFS: assigning an existing object or null costs 0 new
// objects, creating one costs 1), so the first goal found is minimal.
// Returns (nil, nil) when no dangerous graph exists within the bound.
func solve(m *model) (*solution, error) {
	buckets := make([][]*state, m.maxObj+1)
	seen := map[string]bool{}
	enqueue := func(cost int, st *state) {
		k := st.key()
		if seen[k] {
			return
		}
		seen[k] = true
		buckets[cost] = append(buckets[cost], st)
	}
	for _, r := range m.roots {
		enqueue(1, &state{nodes: []node{newNode(m, r)}})
	}
	for cost := 1; cost <= m.maxObj; cost++ {
		for i := 0; i < len(buckets[cost]); i++ {
			if len(seen) > maxExplore {
				return nil, fmt.Errorf("state space too large (> %d states), giving up", maxExplore)
			}
			st := buckets[cost][i]
			if shared := sharedObjects(m, st.nodes); len(shared) > 0 {
				st.complete()
				return &solution{nodes: st.nodes, shared: shared}, nil
			}
			ni, fi, ok := st.firstUnfilled()
			if !ok {
				continue // complete graph, but not dangerous
			}
			staticT := m.typeIdx[m.spec.Types[st.nodes[ni].typ].Fields[fi].Type]
			// Option 1: null.
			ns := st.clone()
			ns.nodes[ni].refs[fi] = refNull
			enqueue(cost, ns)
			// Option 2: reference an existing object (enables sharing + cycles).
			for v := range st.nodes {
				if m.allowedSet[staticT][st.nodes[v].typ] {
					ns := st.clone()
					ns.nodes[ni].refs[fi] = v
					enqueue(cost, ns)
				}
			}
			// Option 3: create a new object of any substitutable type.
			if cost < m.maxObj {
				for _, t := range m.allowed[staticT] {
					ns := st.clone()
					ns.nodes[ni].refs[fi] = len(ns.nodes)
					ns.nodes = append(ns.nodes, newNode(m, t))
					enqueue(cost+1, ns)
				}
			}
		}
		buckets[cost] = nil
	}
	return nil, nil
}
