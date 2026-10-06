package certcache

import "errors"

var (
	ErrNegativeWeight = errors.New("certcache: negative edge weights are not supported")
	ErrEdgeExists     = errors.New("certcache: edge already exists")
	ErrNoEdge         = errors.New("certcache: edge does not exist")
)

// maxVersionValue is the largest value a Version.Value can take before it
// wraps around and forces a graph-wide generation bump.
const maxVersionValue = 255

// Version identifies one incarnation of an edge. Value is incremented on
// every mutation of the edge and wraps after maxVersionValue; Gen is a
// graph-wide generation that is bumped whenever any Value wraps, so that
// certificates recorded before a wraparound are rejected instead of silently
// matching a recycled value.
type Version struct {
	Value uint8
	Gen   uint64
}

// EdgeKey identifies a directed edge.
type EdgeKey struct {
	From, To int
}

// Edge is a weighted directed edge carrying its current version.
type Edge struct {
	Key     EdgeKey
	Weight  float64
	Version Version
}

// Graph is a directed weighted graph whose edges carry versions. Every
// mutation notifies the attached cache so certificates can be invalidated.
type Graph struct {
	edges map[EdgeKey]*Edge
	out   map[int][]EdgeKey
	// versions keeps the last assigned version per edge key, even after the
	// edge is deleted, so a re-added edge never reuses an old version.
	versions map[EdgeKey]Version
	gen      uint64
	adds     uint64
	cache    *Cache
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{
		edges:    make(map[EdgeKey]*Edge),
		out:      make(map[int][]EdgeKey),
		versions: make(map[EdgeKey]Version),
	}
}

// Edge returns the edge for key, if present.
func (g *Graph) Edge(key EdgeKey) (*Edge, bool) {
	e, ok := g.edges[key]
	return e, ok
}

// Adds reports how many edges have been added over the graph's lifetime.
// Shortest-path certificates record it to detect additions.
func (g *Graph) Adds() uint64 { return g.adds }

// AddEdge inserts a new edge. Adding an edge invalidates shortest-path
// certificates but never reachability certificates.
func (g *Graph) AddEdge(from, to int, w float64) error {
	if w < 0 {
		return ErrNegativeWeight
	}
	key := EdgeKey{from, to}
	if _, ok := g.edges[key]; ok {
		return ErrEdgeExists
	}
	g.edges[key] = &Edge{Key: key, Weight: w, Version: g.bumpVersion(key)}
	g.out[from] = append(g.out[from], key)
	g.adds++
	if g.cache != nil {
		g.cache.onEdgeAdded(key)
	}
	return nil
}

// RemoveEdge deletes an edge, invalidating exactly the certificates that
// depend on it. The edge's version tombstone is kept so a later re-addition
// continues the version sequence instead of restarting it.
func (g *Graph) RemoveEdge(from, to int) error {
	key := EdgeKey{from, to}
	if _, ok := g.edges[key]; !ok {
		return ErrNoEdge
	}
	delete(g.edges, key)
	keys := g.out[from]
	for i, k := range keys {
		if k == key {
			g.out[from] = append(keys[:i], keys[i+1:]...)
			break
		}
	}
	if g.cache != nil {
		g.cache.onEdgeRemoved(key)
	}
	return nil
}

// SetWeight changes an edge's weight and bumps its version, invalidating
// exactly the certificates that depend on it.
func (g *Graph) SetWeight(from, to int, w float64) error {
	if w < 0 {
		return ErrNegativeWeight
	}
	key := EdgeKey{from, to}
	e, ok := g.edges[key]
	if !ok {
		return ErrNoEdge
	}
	e.Weight = w
	e.Version = g.bumpVersion(key)
	if g.cache != nil {
		g.cache.onEdgeChanged(key)
	}
	return nil
}

// bumpVersion returns the next version for key. Versions are monotonic per
// edge key, even across delete/re-add cycles. When Value would wrap past
// maxVersionValue, the graph-wide generation is bumped and every cached
// certificate is flushed, because stale recorded values could collide with
// recycled ones.
func (g *Graph) bumpVersion(key EdgeKey) Version {
	last, ok := g.versions[key]
	var next Version
	switch {
	case !ok || last.Gen != g.gen:
		next = Version{Value: 0, Gen: g.gen}
	case last.Value < maxVersionValue:
		next = Version{Value: last.Value + 1, Gen: g.gen}
	default:
		g.gen++
		next = Version{Value: 0, Gen: g.gen}
		if g.cache != nil {
			g.cache.onGenerationBump()
		}
	}
	g.versions[key] = next
	return next
}
