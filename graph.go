package dynclique

import (
	"sort"
	"strconv"
	"strings"
)

// Graph is an undirected dynamic graph that incrementally maintains the
// set of its maximal cliques as edges are added and removed.
//
// Invariant: after every mutation, cliques holds exactly the maximal
// cliques of the current graph, each sorted in ascending order.
type Graph struct {
	adj     map[int]map[int]struct{}
	cliques map[string][]int
}

// NewGraph returns an empty dynamic graph.
func NewGraph() *Graph {
	return &Graph{
		adj:     make(map[int]map[int]struct{}),
		cliques: make(map[string][]int),
	}
}

// AddNode inserts an isolated vertex. A singleton vertex is itself a
// maximal clique until it gains an edge.
func (g *Graph) AddNode(v int) {
	if _, ok := g.adj[v]; ok {
		return
	}
	g.adj[v] = make(map[int]struct{})
	g.cliques[cliqueKey([]int{v})] = []int{v}
}

// HasEdge reports whether the edge (u, v) exists.
func (g *Graph) HasEdge(u, v int) bool {
	_, ok := g.adj[u][v]
	return ok
}

// Nodes returns the vertices of the graph in ascending order.
func (g *Graph) Nodes() []int {
	nodes := make([]int, 0, len(g.adj))
	for v := range g.adj {
		nodes = append(nodes, v)
	}
	sort.Ints(nodes)
	return nodes
}

// AddEdge inserts the edge (u, v) and updates the maximal clique set:
//
//   - Every new maximal clique born by this edge has the form
//     {u, v} ∪ M, where M is a maximal clique of the subgraph induced by
//     the common neighbors of u and v. These are enumerated with
//     Bron-Kerbosch restricted to that subgraph.
//   - Every old maximal clique now contained in a newborn clique is
//     absorbed and removed.
func (g *Graph) AddEdge(u, v int) {
	if u == v {
		panic("dynclique: self-loops are not allowed")
	}
	g.AddNode(u)
	g.AddNode(v)
	if g.HasEdge(u, v) {
		return
	}
	g.adj[u][v] = struct{}{}
	g.adj[v][u] = struct{}{}

	common := intersect(g.adj[u], g.adj[v])
	sub := make(map[int]map[int]struct{}, len(common))
	for w := range common {
		sub[w] = intersect(common, g.adj[w])
	}
	var born [][]int
	for _, m := range MaximalCliques(sub) {
		clique := make([]int, 0, len(m)+2)
		clique = append(clique, u, v)
		clique = append(clique, m...)
		sort.Ints(clique)
		born = append(born, clique)
	}
	// Absorb old cliques contained in a newborn clique.
	for key, old := range g.cliques {
		for _, c := range born {
			if isSubset(old, c) {
				delete(g.cliques, key)
				break
			}
		}
	}
	for _, c := range born {
		g.cliques[cliqueKey(c)] = c
	}
}

// RemoveEdge deletes the edge (u, v) and updates the maximal clique set:
//
//   - Every maximal clique containing both u and v dissolves.
//   - For each dissolved clique K, the candidates K\{u} and K\{v} become
//     maximal unless they are contained in another surviving maximal
//     clique.
func (g *Graph) RemoveEdge(u, v int) {
	if !g.HasEdge(u, v) {
		return
	}
	delete(g.adj[u], v)
	delete(g.adj[v], u)

	var dissolved [][]int
	for key, c := range g.cliques {
		if contains(c, u) && contains(c, v) {
			delete(g.cliques, key)
			dissolved = append(dissolved, c)
		}
	}
	for _, c := range dissolved {
		for _, drop := range []int{u, v} {
			candidate := removeElement(c, drop)
			if len(candidate) == 0 {
				continue
			}
			key := cliqueKey(candidate)
			if _, ok := g.cliques[key]; ok {
				continue
			}
			if g.containedInAny(candidate) {
				continue
			}
			g.cliques[key] = candidate
		}
	}
}

// MaximalCliques returns all maximal cliques currently maintained by the
// graph, each sorted ascending, in deterministic order.
func (g *Graph) MaximalCliques() [][]int {
	out := make([][]int, 0, len(g.cliques))
	for _, c := range g.cliques {
		out = append(out, c)
	}
	sortCliques(out)
	return out
}

// CliquesContaining returns every maximal clique that contains all of the
// given nodes, in deterministic order.
func (g *Graph) CliquesContaining(nodes ...int) [][]int {
	want := append([]int(nil), nodes...)
	sort.Ints(want)
	var out [][]int
	for _, c := range g.cliques {
		if isSubset(want, c) {
			out = append(out, c)
		}
	}
	sortCliques(out)
	return out
}

// containedInAny reports whether candidate is a subset of any currently
// maintained maximal clique.
func (g *Graph) containedInAny(candidate []int) bool {
	for _, c := range g.cliques {
		if isSubset(candidate, c) {
			return true
		}
	}
	return false
}

// isSubset reports whether every element of a (sorted) appears in b (sorted).
func isSubset(a, b []int) bool {
	if len(a) > len(b) {
		return false
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case a[i] > b[j]:
			j++
		default:
			return false
		}
	}
	return i == len(a)
}

func contains(sorted []int, v int) bool {
	i := sort.SearchInts(sorted, v)
	return i < len(sorted) && sorted[i] == v
}

func removeElement(sorted []int, v int) []int {
	out := make([]int, 0, len(sorted)-1)
	for _, x := range sorted {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func cliqueKey(sorted []int) string {
	var sb strings.Builder
	for i, v := range sorted {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(v))
	}
	return sb.String()
}
