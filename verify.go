package certcache

// VerifyReachCert fully re-verifies a reachability certificate against the
// current graph: every path edge must still exist and the edges must chain
// from Pair.From to Pair.To.
func VerifyReachCert(g *Graph, cert *ReachCert) bool {
	end, _, ok := walkPath(g, cert.Pair.From, cert.Path)
	return ok && end == cert.Pair.To
}

// VerifySPCert fully re-verifies a shortest-path certificate: the certified
// path must still exist with cost Dist, and a from-scratch Dijkstra run must
// confirm Dist is the true shortest distance.
func VerifySPCert(g *Graph, cert *SPCert) bool {
	end, cost, ok := walkPath(g, cert.Pair.From, cert.Path)
	if !ok || end != cert.Pair.To || cost != cert.Dist {
		return false
	}
	dist, _, _, found := dijkstra(g, cert.Pair.From, cert.Pair.To)
	return found && dist == cert.Dist
}

// walkPath follows path from start, returning the end node and total cost.
// It fails if an edge is missing or the edges do not chain.
func walkPath(g *Graph, start int, path []EdgeKey) (end int, cost float64, ok bool) {
	node := start
	for _, key := range path {
		if key.From != node {
			return 0, 0, false
		}
		edge, exists := g.edges[key]
		if !exists {
			return 0, 0, false
		}
		cost += edge.Weight
		node = key.To
	}
	return node, cost, true
}
