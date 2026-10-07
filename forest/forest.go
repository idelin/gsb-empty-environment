// Package forest maintains an undirected forest (a disjoint union of trees)
// under edge insertions, edge deletions and connectivity queries.
//
// Inserting an edge that would create a cycle is rejected and leaves the
// forest unchanged. Deleting an edge that does not exist is an error.
// Connectivity is verified by BFS over the adjacency lists.
package forest

import "errors"

var (
	// ErrCycle is returned when adding an edge whose endpoints are already
	// connected (including self-loops); the forest is left unchanged.
	ErrCycle = errors.New("forest: edge would create a cycle")
	// ErrDuplicateEdge is returned when the edge already exists.
	ErrDuplicateEdge = errors.New("forest: edge already exists")
	// ErrEdgeNotFound is returned when deleting an edge that does not exist.
	ErrEdgeNotFound = errors.New("forest: edge does not exist")
)

// Forest is an undirected graph kept acyclic by construction.
// The zero value is ready to use.
type Forest struct {
	adj map[int]map[int]struct{}
}

// New returns an empty forest.
func New() *Forest {
	return &Forest{adj: make(map[int]map[int]struct{})}
}

func (f *Forest) ensureAdj() {
	if f.adj == nil {
		f.adj = make(map[int]map[int]struct{})
	}
}

// HasEdge reports whether the undirected edge (u, v) exists.
// Edge identity is direction-independent: (u, v) and (v, u) are the same edge.
func (f *Forest) HasEdge(u, v int) bool {
	_, ok := f.adj[u][v]
	return ok
}

// Connected reports whether u and v are in the same tree, verified by BFS
// over the current adjacency lists.
func (f *Forest) Connected(u, v int) bool {
	if u == v {
		return true
	}
	visited := map[int]bool{u: true}
	queue := []int{u}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for y := range f.adj[x] {
			if y == v {
				return true
			}
			if !visited[y] {
				visited[y] = true
				queue = append(queue, y)
			}
		}
	}
	return false
}

// AddEdge inserts the undirected edge (u, v).
// It fails without modifying the forest when the edge already exists
// (ErrDuplicateEdge) or when the endpoints are already connected, which
// would create a cycle (ErrCycle, also covers u == v).
func (f *Forest) AddEdge(u, v int) error {
	f.ensureAdj()
	if f.HasEdge(u, v) {
		return ErrDuplicateEdge
	}
	if f.Connected(u, v) {
		return ErrCycle
	}
	if f.adj[u] == nil {
		f.adj[u] = make(map[int]struct{})
	}
	if f.adj[v] == nil {
		f.adj[v] = make(map[int]struct{})
	}
	f.adj[u][v] = struct{}{}
	f.adj[v][u] = struct{}{}
	return nil
}

// RemoveEdge deletes the undirected edge (u, v). It returns ErrEdgeNotFound
// when the edge does not exist; (v, u) identifies the same edge as (u, v).
func (f *Forest) RemoveEdge(u, v int) error {
	if !f.HasEdge(u, v) {
		return ErrEdgeNotFound
	}
	delete(f.adj[u], v)
	delete(f.adj[v], u)
	return nil
}

// EdgeCount returns the number of undirected edges in the forest.
func (f *Forest) EdgeCount() int {
	n := 0
	for _, nbrs := range f.adj {
		n += len(nbrs)
	}
	return n / 2
}
