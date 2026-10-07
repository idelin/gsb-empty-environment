package traj

import "sort"

// FindRoute returns the lexicographically smallest Eulerian trail over
// edges (by edge-ID sequence) that starts at start and uses every edge
// exactly once. When no trail exists it returns a Diagnosis explaining
// the degree or connectivity failure.
//
// The search is greedy with a feasibility look-ahead: at every site the
// remaining outgoing edges are tried in ascending ID order, and a
// candidate is accepted only if the leftover edges still admit an
// Eulerian trail from the candidate's destination. This prevents a
// locally attractive edge from stranding the rest of the graph.
func FindRoute(edges []Edge, start string) ([]Edge, *Diagnosis) {
	if d := checkEuler(edges, start); d != nil {
		return nil, d
	}
	remaining := make([]Edge, len(edges))
	copy(remaining, edges)
	route := make([]Edge, 0, len(edges))
	current := start
	for len(remaining) > 0 {
		var candidates []Edge
		for _, e := range remaining {
			if e.From == current {
				candidates = append(candidates, e)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].ID < candidates[j].ID
		})
		chosen := -1
		for i, c := range candidates {
			rest := removeEdge(remaining, c)
			if checkEuler(rest, c.To) == nil {
				chosen = i
				remaining = rest
				break
			}
		}
		if chosen < 0 {
			// Unreachable: checkEuler passed for the whole set, so a
			// feasible continuation always exists.
			return nil, &Diagnosis{Kind: FailConnectivity, Message: "internal: no feasible continuation"}
		}
		route = append(route, candidates[chosen])
		current = candidates[chosen].To
	}
	return route, nil
}

// removeEdge deletes the first edge equal to target (all three fields)
// from edges and returns the shortened slice.
func removeEdge(edges []Edge, target Edge) []Edge {
	for i, e := range edges {
		if e == target {
			out := make([]Edge, 0, len(edges)-1)
			out = append(out, edges[:i]...)
			out = append(out, edges[i+1:]...)
			return out
		}
	}
	return edges
}

// RouteIDs extracts the edge-ID sequence of a route.
func RouteIDs(route []Edge) []string {
	ids := make([]string, len(route))
	for i, e := range route {
		ids[i] = e.ID
	}
	return ids
}
