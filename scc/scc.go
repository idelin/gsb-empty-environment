// Package scc implements an incremental strongly-connected-components
// maintainer for a dynamic directed graph.
//
// The maintainer supports vertex add/remove and directed edge
// insert/delete. After every mutation it reports the SCC delta:
// which old components merged into a new one, and which component
// split into several new ones. Component IDs are stable: the ID of a
// component is the smallest node ID it contains.
//
// Internally it keeps a condensed (per-component) view and only runs
// local traversals around the touched edge: merges are computed by
// intersecting one forward and one backward reachability walk, splits
// by re-running Tarjan on the single affected component.
package scc

import "sort"

// Component is a strongly connected component. ID is the smallest
// node ID among its members, which keeps it stable across updates.
type Component struct {
	ID    string
	Nodes []string // sorted node IDs
}

// Merge describes a set of old components collapsing into one new
// component after an edge insertion.
type Merge struct {
	From []string  // sorted IDs of the old components that disappeared
	To   Component // the resulting merged component
}

// Split describes one component breaking apart into several new
// components after an edge or vertex removal.
type Split struct {
	From string      // ID of the component that disappeared
	To   []Component // resulting components, sorted by ID
}

// Change is the SCC delta produced by a single graph mutation.
type Change struct {
	Merges []Merge
	Splits []Split
}

// Empty reports whether the mutation left the SCC partition unchanged.
func (c Change) Empty() bool { return len(c.Merges) == 0 && len(c.Splits) == 0 }

func (c *Change) absorb(o Change) {
	c.Merges = append(c.Merges, o.Merges...)
	c.Splits = append(c.Splits, o.Splits...)
}

// Maintainer tracks the SCCs of a directed graph under updates.
type Maintainer struct {
	adj   map[string]map[string]bool // node -> out-neighbours
	radj  map[string]map[string]bool // node -> in-neighbours
	comp  map[string]string          // node -> component ID
	comps map[string]map[string]bool // component ID -> member set
}

// New returns an empty Maintainer.
func New() *Maintainer {
	return &Maintainer{
		adj:   make(map[string]map[string]bool),
		radj:  make(map[string]map[string]bool),
		comp:  make(map[string]string),
		comps: make(map[string]map[string]bool),
	}
}

// AddVertex inserts an isolated vertex. It is a no-op if the vertex
// already exists. The vertex starts as a singleton component.
func (m *Maintainer) AddVertex(v string) Change {
	if _, ok := m.comp[v]; ok {
		return Change{}
	}
	m.adj[v] = make(map[string]bool)
	m.radj[v] = make(map[string]bool)
	m.comp[v] = v
	m.comps[v] = map[string]bool{v: true}
	return Change{}
}

// RemoveVertex deletes a vertex together with all its incident edges
// and reports the resulting SCC delta (its old component may split).
func (m *Maintainer) RemoveVertex(v string) Change {
	cid, ok := m.comp[v]
	if !ok {
		return Change{}
	}
	for w := range m.adj[v] {
		delete(m.radj[w], v)
	}
	for u := range m.radj[v] {
		delete(m.adj[u], v)
	}
	delete(m.adj, v)
	delete(m.radj, v)
	delete(m.comp, v)
	delete(m.comps[cid], v)

	var ch Change
	rest := m.comps[cid]
	if len(rest) == 0 {
		delete(m.comps, cid)
		return ch
	}
	// The remaining members may no longer be strongly connected.
	sccs := m.localSCCs(rest)
	if len(sccs) == 1 && sccs[0][0] == cid {
		return ch // same members, same ID: nothing changed
	}
	ch.Splits = append(ch.Splits, m.replaceComponent(cid, sccs))
	return ch
}

// AddEdge inserts the directed edge u->v, auto-creating missing
// endpoints, and reports any component merge it triggers.
func (m *Maintainer) AddEdge(u, v string) Change {
	var ch Change
	ch.absorb(m.AddVertex(u))
	ch.absorb(m.AddVertex(v))
	if m.adj[u][v] {
		return ch
	}
	m.adj[u][v] = true
	m.radj[v][u] = true

	cu, cv := m.comp[u], m.comp[v]
	if cu == cv {
		return ch // internal edge: SCC partition unchanged
	}
	// The edge only merges components if it closes a cycle, i.e. v
	// already reaches u. The merged component is exactly the set of
	// nodes reachable from v that can also reach u.
	if !m.reachable(v, u) {
		return ch
	}
	fwd := m.walk(v, m.adj)
	bwd := m.walk(u, m.radj)
	members := make(map[string]bool)
	for n := range fwd {
		if bwd[n] {
			members[n] = true
		}
	}
	ch.Merges = append(ch.Merges, m.mergeMembers(members))
	return ch
}

// RemoveEdge deletes the directed edge u->v and reports any component
// split it triggers. Missing edges are a no-op.
func (m *Maintainer) RemoveEdge(u, v string) Change {
	var ch Change
	if !m.adj[u][v] {
		return ch
	}
	delete(m.adj[u], v)
	delete(m.radj[v], u)

	if m.comp[u] != m.comp[v] {
		return ch // cross-component edge: SCC partition unchanged
	}
	cid := m.comp[u]
	sccs := m.localSCCs(m.comps[cid])
	if len(sccs) == 1 {
		return ch // still strongly connected
	}
	ch.Splits = append(ch.Splits, m.replaceComponent(cid, sccs))
	return ch
}

// ComponentOf returns the component currently containing node v.
func (m *Maintainer) ComponentOf(v string) (Component, bool) {
	cid, ok := m.comp[v]
	if !ok {
		return Component{}, false
	}
	return m.component(cid), true
}

// Components returns all current components sorted by ID.
func (m *Maintainer) Components() []Component {
	out := make([]Component, 0, len(m.comps))
	for cid := range m.comps {
		out = append(out, m.component(cid))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// HasVertex reports whether v exists in the graph.
func (m *Maintainer) HasVertex(v string) bool {
	_, ok := m.comp[v]
	return ok
}

// HasEdge reports whether the edge u->v exists.
func (m *Maintainer) HasEdge(u, v string) bool {
	return m.adj[u][v]
}

func (m *Maintainer) component(cid string) Component {
	nodes := sortedKeys(m.comps[cid])
	return Component{ID: cid, Nodes: nodes}
}

// mergeMembers collapses every component touching members into a
// single new component and returns the Merge record.
func (m *Maintainer) mergeMembers(members map[string]bool) Merge {
	fromSet := make(map[string]bool)
	for n := range members {
		fromSet[m.comp[n]] = true
	}
	from := sortedKeys(fromSet)

	newID := minKey(members)
	delete(m.comps, newID)
	for _, cid := range from {
		if cid != newID {
			delete(m.comps, cid)
		}
	}
	m.comps[newID] = make(map[string]bool, len(members))
	for n := range members {
		m.comp[n] = newID
		m.comps[newID][n] = true
	}
	return Merge{From: from, To: m.component(newID)}
}

// replaceComponent removes component cid and installs the given SCCs
// (a partition of its members) as new components, returning the Split
// record. Each new component takes its smallest member as ID.
func (m *Maintainer) replaceComponent(cid string, sccs [][]string) Split {
	delete(m.comps, cid)
	sp := Split{From: cid}
	for _, scc := range sccs {
		id := scc[0] // scc members are sorted
		set := make(map[string]bool, len(scc))
		for _, n := range scc {
			m.comp[n] = id
			set[n] = true
		}
		m.comps[id] = set
		sp.To = append(sp.To, Component{ID: id, Nodes: scc})
	}
	sort.Slice(sp.To, func(i, j int) bool { return sp.To[i].ID < sp.To[j].ID })
	return sp
}

// localSCCs runs Tarjan's algorithm on the subgraph induced by
// members and returns its SCCs with sorted member lists.
func (m *Maintainer) localSCCs(members map[string]bool) [][]string {
	index := make(map[string]int, len(members))
	low := make(map[string]int, len(members))
	onStack := make(map[string]bool, len(members))
	var stack []string
	var sccs [][]string
	counter := 0

	var visit func(v string)
	visit = func(v string) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range sortedKeys(m.adj[v]) {
			if !members[w] {
				continue
			}
			if _, seen := index[w]; !seen {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			sccs = append(sccs, scc)
		}
	}
	for _, v := range sortedKeys(members) {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	return sccs
}

// reachable reports whether target is reachable from start.
func (m *Maintainer) reachable(start, target string) bool {
	if start == target {
		return true
	}
	seen := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for w := range m.adj[v] {
			if w == target {
				return true
			}
			if !seen[w] {
				seen[w] = true
				stack = append(stack, w)
			}
		}
	}
	return false
}

// walk returns the set of nodes reachable from start following g.
func (m *Maintainer) walk(start string, g map[string]map[string]bool) map[string]bool {
	seen := map[string]bool{start: true}
	stack := []string{start}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for w := range g[v] {
			if !seen[w] {
				seen[w] = true
				stack = append(stack, w)
			}
		}
	}
	return seen
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func minKey(set map[string]bool) string {
	first := true
	var min string
	for k := range set {
		if first || k < min {
			min = k
			first = false
		}
	}
	return min
}
