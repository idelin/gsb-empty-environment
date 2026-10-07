package certcache

// CertKind discriminates the certificate types managed by the cache.
type CertKind int

const (
	// KindReachability certifies that a path exists between two nodes.
	// Its witness is the path itself; it stays valid under edge
	// insertions and is only invalidated when one of its dependency
	// edges is removed or changed.
	KindReachability CertKind = iota
	// KindShortestPath certifies the minimum distance between two nodes.
	// Besides its dependency edges it is tied to the graph's add epoch:
	// any newly inserted edge may yield a shorter path, so insertion
	// invalidates all shortest-path certificates.
	KindShortestPath
)

func (k CertKind) String() string {
	switch k {
	case KindReachability:
		return "reachability"
	case KindShortestPath:
		return "shortest-path"
	}
	return "unknown"
}

// Query identifies a cached source-to-target question.
type Query struct {
	From string
	To   string
}

// Dependency pins one edge version a certificate relies on.
type Dependency struct {
	Edge    EdgeKey
	Version uint64
}

// Certificate is a cached, independently verifiable answer to a Query.
type Certificate struct {
	Kind  CertKind
	Query Query
	// Path is the witness: the ordered edges from Query.From to Query.To.
	Path []EdgeKey
	// Distance is the certified value: path length in hops for
	// reachability, total weight for shortest-path.
	Distance int64
	// Deps are the edge versions the certificate was computed against.
	Deps []Dependency
	// AddEpoch is the graph insertion epoch at computation time. Only
	// meaningful for shortest-path certificates.
	AddEpoch uint64
}

// dependsOn reports whether the certificate relies on the given edge.
func (c *Certificate) dependsOn(key EdgeKey) bool {
	for _, d := range c.Deps {
		if d.Edge == key {
			return true
		}
	}
	return false
}
