package certcache

// Engine answers reachability and shortest-path queries, caching
// certificates and serving repeat queries from them while they stay valid.
type Engine struct {
	G     *Graph
	Cache *Cache

	ReachHits, ReachMisses int
	SPHits, SPMisses       int
}

// NewEngine attaches a fresh cache to g and returns the query engine.
func NewEngine(g *Graph) *Engine {
	c := NewCache()
	g.cache = c
	return &Engine{G: g, Cache: c}
}

// Reachable reports whether to is reachable from from. Positive answers are
// certified by the discovered path and served from the cache on repeats.
func (e *Engine) Reachable(from, to int) bool {
	p := Pair{from, to}
	if _, ok := e.Cache.getReach(e.G, p); ok {
		e.ReachHits++
		return true
	}
	e.ReachMisses++
	path, found := bfsPath(e.G, from, to)
	if !found {
		return false
	}
	e.Cache.putReach(&ReachCert{Pair: p, Path: path, Deps: depsOf(e.G, path)})
	return true
}

// ShortestPath returns the shortest distance from from to to, the path
// achieving it, and whether such a path exists.
func (e *Engine) ShortestPath(from, to int) (float64, []EdgeKey, bool) {
	p := Pair{from, to}
	if cert, ok := e.Cache.getSP(e.G, p); ok {
		e.SPHits++
		return cert.Dist, cert.Path, true
	}
	e.SPMisses++
	dist, path, deps, found := dijkstra(e.G, from, to)
	if !found {
		return 0, nil, false
	}
	e.Cache.putSP(&SPCert{Pair: p, Path: path, Dist: dist, Deps: deps, Adds: e.G.adds})
	return dist, path, true
}

// depsOf snapshots the current versions of the path's edges.
func depsOf(g *Graph, path []EdgeKey) map[EdgeKey]Version {
	deps := make(map[EdgeKey]Version, len(path))
	for _, key := range path {
		if e, ok := g.edges[key]; ok {
			deps[key] = e.Version
		}
	}
	return deps
}

// bfsPath finds a path from from to to via breadth-first search.
func bfsPath(g *Graph, from, to int) ([]EdgeKey, bool) {
	visited := map[int]bool{from: true}
	prev := make(map[int]EdgeKey)
	queue := []int{from}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if u == to {
			return reconstruct(prev, from, to), true
		}
		for _, key := range g.out[u] {
			if !visited[key.To] {
				visited[key.To] = true
				prev[key.To] = key
				queue = append(queue, key.To)
			}
		}
	}
	return nil, false
}

// dijkstra computes the shortest distance from from to to over non-negative
// weights. It also returns the certificate dependency set: the out-edges of
// every node settled before the target. With non-negative weights, no edge
// outside that set can influence the distance, so a weight change or
// deletion touching only outside edges leaves the certificate valid.
func dijkstra(g *Graph, from, to int) (float64, []EdgeKey, map[EdgeKey]Version, bool) {
	dist := map[int]float64{from: 0}
	prev := make(map[int]EdgeKey)
	deps := make(map[EdgeKey]Version)
	settled := make(map[int]bool)
	for {
		u := -1
		var best float64
		for node, d := range dist {
			if !settled[node] && (u == -1 || d < best) {
				u, best = node, d
			}
		}
		if u == -1 {
			return 0, nil, nil, false
		}
		settled[u] = true
		if u == to {
			return best, reconstruct(prev, from, to), deps, true
		}
		for _, key := range g.out[u] {
			edge := g.edges[key]
			deps[key] = edge.Version
			nd := best + edge.Weight
			if cur, ok := dist[key.To]; !ok || nd < cur {
				dist[key.To] = nd
				prev[key.To] = key
			}
		}
	}
}

// reconstruct rebuilds the edge path from from to to using the prev map.
func reconstruct(prev map[int]EdgeKey, from, to int) []EdgeKey {
	var rev []EdgeKey
	for node := to; node != from; {
		key := prev[node]
		rev = append(rev, key)
		node = key.From
	}
	path := make([]EdgeKey, len(rev))
	for i, key := range rev {
		path[len(rev)-1-i] = key
	}
	return path
}
