package certcache

import "sort"

// verify.go holds the ground-truth recomputation routines (full
// re-verification) and certificate validation. The cache answers queries
// from certificates; these routines recompute answers from scratch so
// tests and callers can cross-check that targeted invalidation never
// leaves a stale certificate behind.

// adjacency builds a deterministically ordered adjacency list from an
// edge snapshot, so recomputation and certificate paths are stable.
func adjacency(edges map[EdgeKey]Edge) map[string][]Edge {
	adj := make(map[string][]Edge)
	for _, e := range edges {
		adj[e.Key.From] = append(adj[e.Key.From], e)
	}
	for from := range adj {
		sort.Slice(adj[from], func(i, j int) bool {
			return adj[from][i].Key.To < adj[from][j].Key.To
		})
	}
	return adj
}

// depsOf builds the dependency list for a witness path.
func depsOf(edges map[EdgeKey]Edge, path []EdgeKey) []Dependency {
	deps := make([]Dependency, 0, len(path))
	for _, key := range path {
		deps = append(deps, Dependency{Edge: key, Version: edges[key].Version})
	}
	return deps
}

// FullReachable recomputes reachability from scratch (BFS) and, when
// reachable, returns a fresh reachability certificate.
func FullReachable(g *Graph, from, to string) (bool, *Certificate) {
	edges, _ := g.snapshot()
	adj := adjacency(edges)

	prev := map[string]EdgeKey{from: {}}
	queue := []string{from}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node == to {
			break
		}
		for _, e := range adj[node] {
			if _, seen := prev[e.Key.To]; !seen {
				prev[e.Key.To] = e.Key
				queue = append(queue, e.Key.To)
			}
		}
	}
	if _, ok := prev[to]; !ok {
		return false, nil
	}
	path := reconstructPath(prev, from, to)
	return true, &Certificate{
		Kind:     KindReachability,
		Query:    Query{From: from, To: to},
		Path:     path,
		Distance: int64(len(path)),
		Deps:     depsOf(edges, path),
	}
}

// FullShortest recomputes the shortest path from scratch (Dijkstra) and,
// when reachable, returns a fresh shortest-path certificate.
func FullShortest(g *Graph, from, to string) (int64, bool, *Certificate) {
	edges, epoch := g.snapshot()
	adj := adjacency(edges)

	const inf = int64(^uint64(0) >> 1)
	dist := map[string]int64{from: 0}
	prev := make(map[string]EdgeKey)
	done := make(map[string]bool)

	for {
		// Pick the unsettled node with the smallest tentative distance.
		node := ""
		best := inf
		for n, d := range dist {
			if !done[n] && d < best {
				best = d
				node = n
			}
		}
		if node == "" {
			break
		}
		done[node] = true
		if node == to {
			break
		}
		for _, e := range adj[node] {
			if e.Weight < 0 {
				continue // Dijkstra assumes non-negative weights
			}
			if nd := dist[node] + e.Weight; nd < getDist(dist, e.Key.To, inf) {
				dist[e.Key.To] = nd
				prev[e.Key.To] = e.Key
			}
		}
	}

	d, ok := dist[to]
	if !ok {
		return 0, false, nil
	}
	path := reconstructPath(prev, from, to)
	return d, true, &Certificate{
		Kind:     KindShortestPath,
		Query:    Query{From: from, To: to},
		Path:     path,
		Distance: d,
		Deps:     depsOf(edges, path),
		AddEpoch: epoch,
	}
}

func getDist(dist map[string]int64, node string, fallback int64) int64 {
	if d, ok := dist[node]; ok {
		return d
	}
	return fallback
}

// reconstructPath walks the predecessor chain from->to.
func reconstructPath(prev map[string]EdgeKey, from, to string) []EdgeKey {
	if from == to {
		return nil
	}
	var rev []EdgeKey
	for node := to; node != from; {
		key, ok := prev[node]
		if !ok {
			return nil
		}
		rev = append(rev, key)
		node = key.From
	}
	path := make([]EdgeKey, len(rev))
	for i, key := range rev {
		path[len(rev)-1-i] = key
	}
	return path
}

// Validate re-verifies a certificate against the current graph state.
//
// A certificate is valid when every dependency edge still exists with the
// exact recorded version (version wraparound is rejected at mutation time,
// so an equal version can never be a recycled one) and the witness path
// still connects the query endpoints. Shortest-path certificates must
// additionally match the current add epoch, since any inserted edge may
// have created a shorter path.
func Validate(g *Graph, cert *Certificate) bool {
	if cert == nil {
		return false
	}
	if cert.Kind == KindShortestPath && cert.AddEpoch != g.AddEpoch() {
		return false
	}
	// A self-query (from == to) has an empty witness and no dependencies.
	if cert.Query.From == cert.Query.To {
		return true
	}
	if len(cert.Path) == 0 || len(cert.Deps) != len(cert.Path) {
		return false
	}
	node := cert.Query.From
	for i, key := range cert.Path {
		if key.From != node {
			return false
		}
		edge := g.Edge(key.From, key.To)
		if edge == nil || edge.Version != cert.Deps[i].Version {
			return false
		}
		node = key.To
	}
	return node == cert.Query.To
}
