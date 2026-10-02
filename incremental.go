package kcore

// Maintainer keeps exact core numbers of a graph up to date under edge
// insertions and deletions. Instead of recomputing the decomposition
// globally, each update re-propagates coreness only inside the affected
// local neighborhood:
//
//   - A single edge insertion can raise a core number by at most 1, and
//     only vertices with core == min(core[u], core[v]) connected to u or
//     v through same-core vertices are candidates.
//   - Symmetrically, a single edge deletion can lower a core number by
//     at most 1, restricted to the same kind of candidate component.
//
// Candidates are then re-peeled locally against the support they get
// from higher-core neighbors and from surviving fellow candidates.
type Maintainer struct {
	g    *Graph
	core map[int]int

	// Statistics of the most recent update, for instrumentation.
	Visited int // vertices touched by the local propagation
	Updated int // vertices whose core number actually changed
}

// NewMaintainer wraps g and computes the initial core numbers with a
// full O(V+E) decomposition. The graph must not be mutated directly
// afterwards; use InsertEdge/RemoveEdge instead.
func NewMaintainer(g *Graph) *Maintainer {
	return &Maintainer{g: g, core: CoreNumbers(g)}
}

// Core returns the current core number of v.
func (m *Maintainer) Core(v int) int { return m.core[v] }

// Cores returns a copy of the current core-number map.
func (m *Maintainer) Cores() map[int]int {
	out := make(map[int]int, len(m.core))
	for v, c := range m.core {
		out[v] = c
	}
	return out
}

// Graph returns the underlying graph (read-only use).
func (m *Maintainer) Graph() *Graph { return m.g }

// InsertEdge adds edge (u,v) and locally re-propagates core numbers.
func (m *Maintainer) InsertEdge(u, v int) {
	m.Visited, m.Updated = 0, 0
	if u == v || m.g.HasEdge(u, v) {
		return
	}
	m.g.AddEdge(u, v)
	k := minInt(m.core[u], m.core[v])
	cand := m.collect(u, v, k)
	if len(cand) == 0 {
		return
	}
	support := m.localSupport(cand, k)
	// Locally peel candidates with fewer than k+1 supporters; the
	// survivors are exactly the vertices promoted to core k+1.
	removed := m.peelLocal(cand, support, k+1)
	for w := range cand {
		if !removed[w] {
			m.core[w] = k + 1
			m.Updated++
		}
	}
}

// RemoveEdge deletes edge (u,v) and locally re-propagates core numbers.
func (m *Maintainer) RemoveEdge(u, v int) {
	m.Visited, m.Updated = 0, 0
	if !m.g.HasEdge(u, v) {
		return
	}
	k := minInt(m.core[u], m.core[v])
	m.g.RemoveEdge(u, v)
	cand := m.collect(u, v, k)
	if len(cand) == 0 {
		return
	}
	support := m.localSupport(cand, k)
	// Locally peel candidates with fewer than k supporters; they drop
	// to core k-1, everyone else keeps core k.
	removed := m.peelLocal(cand, support, k)
	for w := range cand {
		if removed[w] {
			m.core[w] = k - 1
			m.Updated++
		}
	}
}

// collect finds the candidate component: vertices with core == k
// reachable from u or v through vertices that also have core == k.
// Only these vertices can change core number after the edge update.
func (m *Maintainer) collect(u, v, k int) map[int]bool {
	cand := make(map[int]bool)
	var queue []int
	push := func(x int) {
		if c, ok := m.core[x]; ok && c == k && !cand[x] {
			cand[x] = true
			queue = append(queue, x)
		}
	}
	push(u)
	push(v)
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		m.Visited++
		for x := range m.g.adj[w] {
			push(x)
		}
	}
	return cand
}

// localSupport counts, for every candidate, the neighbors that can
// support a higher core level: vertices already above k, plus fellow
// candidates (whose survival is decided by the local peel).
func (m *Maintainer) localSupport(cand map[int]bool, k int) map[int]int {
	support := make(map[int]int, len(cand))
	for w := range cand {
		s := 0
		for x := range m.g.adj[w] {
			if cand[x] || m.core[x] > k {
				s++
			}
		}
		support[w] = s
	}
	return support
}

// peelLocal removes candidates whose support falls below threshold,
// cascading the loss to neighboring candidates, and returns the removed
// set. It is the local counterpart of PeelKCore.
func (m *Maintainer) peelLocal(cand map[int]bool, support map[int]int, threshold int) map[int]bool {
	removed := make(map[int]bool)
	var queue []int
	for w := range cand {
		if support[w] < threshold {
			queue = append(queue, w)
		}
	}
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		if removed[w] {
			continue
		}
		removed[w] = true
		for x := range m.g.adj[w] {
			if !cand[x] || removed[x] {
				continue
			}
			support[x]--
			if support[x] < threshold {
				queue = append(queue, x)
			}
		}
	}
	return removed
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
