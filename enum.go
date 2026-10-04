package main

// Enumerator lists all minimum s-t cuts of a directed network as vertex
// sets, in lexicographic order of their ascending vertex lists
// (a list that is a prefix of another sorts first).
//
// Theory (Picard-Queyranne): after a maximum flow is computed, the minimum
// s-t cuts are exactly the vertex sets S such that s in S, t not in S, and
// S is closed under reachability in the residual graph. Many different
// residual paths can lead to the same closed set, so the enumerator never
// follows residual paths directly. Instead it walks the trie of ascending
// vertex lists: a node is a prefix P, and a child extends P with the next
// vertex b > max(P). A prefix is feasible iff some closed set has exactly
// that prefix. Because every cut has exactly one ascending vertex list,
// every cut is produced exactly once, and emission order is lexicographic.
type Enumerator struct {
	adj [][]int // residual adjacency of a maximum flow
	n   int
	s   int
	t   int

	inPrefix []bool
	prefix   []int // ascending 0-based vertices
}

func NewEnumerator(adj [][]int, n, s, t int) *Enumerator {
	return &Enumerator{adj: adj, n: n, s: s, t: t}
}

// reachable returns the set of vertices reachable from the seed set.
func (e *Enumerator) reachable(seed []bool) []bool {
	seen := make([]bool, e.n)
	stack := make([]int, 0, e.n)
	for v := 0; v < e.n; v++ {
		if seed[v] {
			seen[v] = true
			stack = append(stack, v)
		}
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, w := range e.adj[u] {
			if !seen[w] {
				seen[w] = true
				stack = append(stack, w)
			}
		}
	}
	return seen
}

// Visit enumerates every minimum cut, invoking emit with each cut as an
// ascending list of 1-based vertices, in lexicographic order. emit may
// return false to stop the enumeration early.
func (e *Enumerator) Visit(emit func(cut []int) bool) {
	e.inPrefix = make([]bool, e.n)
	e.prefix = e.prefix[:0]
	e.visit(emit)
}

// visit processes the trie node for the current prefix. The caller
// guarantees the prefix is feasible. Returns false if emit stopped.
func (e *Enumerator) visit(emit func([]int) bool) bool {
	seed := make([]bool, e.n)
	seed[e.s] = true
	for _, v := range e.prefix {
		seed[v] = true
	}
	closure := e.reachable(seed)
	if closure[e.t] {
		// Infeasible prefix; cannot happen if callers filter correctly.
		return true
	}
	// The prefix itself is a minimum cut iff it contains s and is closed
	// under residual reachability.
	if e.inPrefix[e.s] && sameSet(closure, e.inPrefix) {
		cut := make([]int, len(e.prefix))
		for i, v := range e.prefix {
			cut[i] = v + 1
		}
		if !emit(cut) {
			return false
		}
	}
	lo := -1
	if len(e.prefix) > 0 {
		lo = e.prefix[len(e.prefix)-1]
	}
	for b := lo + 1; b < e.n; b++ {
		if e.inPrefix[b] {
			continue
		}
		seed[b] = true
		childClosure := e.reachable(seed)
		seed[b] = false
		if childClosure[e.t] {
			continue // every superset would contain t
		}
		// prefix+[b] is feasible iff the smallest closed superset of
		// prefix+{b}+{s} introduces no vertex <= b outside prefix+{b};
		// otherwise no cut has exactly this ascending prefix.
		feasible := true
		for v := 0; v <= b; v++ {
			want := e.inPrefix[v] || v == b
			if childClosure[v] != want {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}
		e.inPrefix[b] = true
		e.prefix = append(e.prefix, b)
		cont := e.visit(emit)
		e.prefix = e.prefix[:len(e.prefix)-1]
		e.inPrefix[b] = false
		if !cont {
			return false
		}
	}
	return true
}

func sameSet(a, b []bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// lexCompare compares two ascending vertex lists lexicographically;
// a strict prefix sorts before its extensions.
func lexCompare(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
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
