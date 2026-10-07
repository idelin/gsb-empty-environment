// Package forest maintains an undirected forest (a disjoint union of trees)
// under edge insertions, edge deletions and connectivity queries.
//
// Inserting an edge that would create a cycle is rejected and leaves the
// forest unchanged. Deleting an edge that does not exist reports an error.
// Edges are undirected: (u, v) and (v, u) denote the same edge.
package forest

import "errors"

var (
	// ErrCycle is returned by Link when the edge would create a cycle.
	// This also covers duplicate edges and self loops.
	ErrCycle = errors.New("forest: link would create a cycle")
	// ErrNoEdge is returned by Cut when the edge does not exist.
	ErrNoEdge = errors.New("forest: edge does not exist")
)

// Forest is a set of trees over integer vertices.
// The zero value is ready to use.
type Forest struct {
	adj map[int]map[int]struct{}
}

// New returns an empty Forest.
func New() *Forest {
	return &Forest{adj: make(map[int]map[int]struct{})}
}

func (f *Forest) ensure() {
	if f.adj == nil {
		f.adj = make(map[int]map[int]struct{})
	}
}

// HasEdge reports whether the undirected edge (u, v) exists.
func (f *Forest) HasEdge(u, v int) bool {
	_, ok := f.adj[u][v]
	return ok
}

// Connected reports whether u and v lie in the same tree.
// It runs a breadth-first search over the adjacency lists.
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

// Link adds the undirected edge (u, v). If u and v are already connected
// (including u == v, i.e. a self loop, or a duplicate edge in either
// direction), the edge would close a cycle: Link returns ErrCycle and the
// forest is left unchanged.
func (f *Forest) Link(u, v int) error {
	if f.Connected(u, v) {
		return ErrCycle
	}
	f.ensure()
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

// Cut removes the undirected edge (u, v). It returns ErrNoEdge if the edge
// is not present; the direction used at Link time does not matter.
func (f *Forest) Cut(u, v int) error {
	if !f.HasEdge(u, v) {
		return ErrNoEdge
	}
	delete(f.adj[u], v)
	delete(f.adj[v], u)
	return nil
}
