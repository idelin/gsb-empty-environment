package roadgraph

import "fmt"

// Replacement describes the shortest path after removing one edge of the
// specified path. Distance is -1 when Reachable is false.
type Replacement struct {
	Removed   EdgeID
	Reachable bool
	Distance  int64
	Path      []EdgeID // one witness shortest path avoiding Removed
}

// ReplacementPaths computes, for every edge of the given source-target path,
// the shortest source-target distance in the graph with that single edge
// removed, plus one witness path. Each removal is checked with a fresh
// Dijkstra run (per-edge Dijkstra). Unreachable results are marked
// explicitly via Reachable=false.
//
// The given path must be a valid chain of edge IDs from source to target;
// parallel edges are disambiguated by ID, so removing one never affects its
// siblings.
func ReplacementPaths(g *Graph, source, target int, path []EdgeID) ([]Replacement, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("replacement paths: empty path")
	}
	cur := source
	for _, id := range path {
		e, ok := g.Edge(id)
		if !ok {
			return nil, fmt.Errorf("replacement paths: unknown edge %d", id)
		}
		if e.From != cur {
			return nil, fmt.Errorf("replacement paths: edge %d starts at %d, want %d", id, e.From, cur)
		}
		cur = e.To
	}
	if cur != target {
		return nil, fmt.Errorf("replacement paths: path ends at %d, want %d", cur, target)
	}

	out := make([]Replacement, len(path))
	for i, id := range path {
		exclude := map[EdgeID]bool{id: true}
		dist, witness, ok := g.ShortestPath(source, target, exclude)
		out[i] = Replacement{Removed: id, Reachable: ok, Distance: -1, Path: nil}
		if ok {
			out[i].Distance = dist
			out[i].Path = witness
		}
	}
	return out, nil
}
