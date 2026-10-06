package certcache

// Pair identifies a (source, target) query.
type Pair struct {
	From, To int
}

// ReachCert certifies that Pair.To is reachable from Pair.From along Path.
// It depends on the exact versions of the path edges: deleting or
// re-weighting any of them invalidates the certificate. Edge additions never
// invalidate it, because reachability is monotone under additions.
type ReachCert struct {
	Pair Pair
	Path []EdgeKey
	Deps map[EdgeKey]Version
}

// SPCert certifies that Dist is the shortest-path distance for Pair. Besides
// the path edges, it depends on every edge relaxed while settling nodes
// closer than the target: with non-negative weights no other edge can
// influence the distance. Any edge addition invalidates the certificate,
// because a new edge may open a shorter route; Adds records the graph's
// addition counter at cache time for that check.
type SPCert struct {
	Pair Pair
	Path []EdgeKey
	Dist float64
	Deps map[EdgeKey]Version
	Adds uint64
}
