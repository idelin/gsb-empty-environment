package graph

import "fmt"

// Replacement describes the network state after removing one edge of the
// reference shortest path. When Reachable is false, t is unreachable from
// s without RemovedEdge and Dist/Witness are meaningless.
type Replacement struct {
	RemovedEdge int   // ID of the removed path edge
	Dist        int64 // shortest s-t distance in G - {RemovedEdge}
	Reachable   bool  // explicit unreachable marker
	Witness     []int // edge IDs of one shortest replacement path
}

// ReplacementPaths solves the replacement paths problem: given a shortest
// s-t path (as a list of edge IDs), it reports, for every edge e of the
// path, the shortest s-t distance and a witness path in G - {e}.
//
// Only the edge with that exact ID is removed; parallel edges between the
// same endpoints stay in the graph. The check is performed by an
// independent Dijkstra run per path edge.
//
// The given path must be a valid s-t chain of existing edge IDs; an error
// is returned otherwise. If s == t the path must be empty and the result
// is empty (there is no edge to remove).
func (g *Graph) ReplacementPaths(s, t int, path []int) ([]Replacement, error) {
	if s < 0 || s >= g.NumNodes || t < 0 || t >= g.NumNodes {
		return nil, fmt.Errorf("graph: node out of range")
	}
	cur := s
	for _, id := range path {
		if id < 0 || id >= len(g.Edges) {
			return nil, fmt.Errorf("graph: invalid edge ID %d in path", id)
		}
		e := g.Edges[id]
		if e.From != cur {
			return nil, fmt.Errorf("graph: path broken at edge %d", id)
		}
		cur = e.To
	}
	if cur != t {
		return nil, fmt.Errorf("graph: path ends at %d, want %d", cur, t)
	}

	out := make([]Replacement, 0, len(path))
	for _, id := range path {
		res := g.ShortestPath(s, t, id)
		out = append(out, Replacement{
			RemovedEdge: id,
			Dist:        res.Dist,
			Reachable:   res.Reachable,
			Witness:     res.Path,
		})
	}
	return out, nil
}
