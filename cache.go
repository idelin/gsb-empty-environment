package roadgraph

import "fmt"

// PathCache memoizes shortest-path answers for a VersionedGraph and
// invalidates them when local revisions arrive.
//
// Shortestness of a cached path depends on more than the edges on the path:
// a revision that lowers the cost of some other, "external" edge can dethrone
// it. The cache therefore records, for every entry, the full dependency set
// of the query — every edge examined while freezing the graph and running
// Dijkstra — and drops an entry whenever a revision touches that set.
type PathCache struct {
	vg      *VersionedGraph
	entries map[Query]*cacheEntry
}

type cacheEntry struct {
	result  QueryResult
	readSet map[EdgeID]bool
}

// NewPathCache returns a cache over vg.
func NewPathCache(vg *VersionedGraph) *PathCache {
	return &PathCache{vg: vg, entries: make(map[Query]*cacheEntry)}
}

// Query returns the cached answer when present, otherwise computes it by
// full recompute and stores it together with its dependency set.
func (c *PathCache) Query(q Query) QueryResult {
	if e, ok := c.entries[q]; ok {
		return e.result
	}
	frozen, considered := c.vg.Freeze(q.Knowledge, q.Depart)
	sr := frozen.ShortestPaths(q.Source, nil)
	readSet := make(map[EdgeID]bool, len(considered)+len(sr.Read))
	for _, id := range considered {
		readSet[id] = true
	}
	for id := range sr.Read {
		readSet[id] = true
	}
	path, ok := sr.PathTo(q.Target)
	res := QueryResult{Reachable: false, Distance: -1}
	if ok {
		res = QueryResult{Reachable: true, Distance: sr.Dist[q.Target], Path: path}
	}
	c.entries[q] = &cacheEntry{result: res, readSet: readSet}
	return res
}

// AddRevision applies a local revision and invalidates every cached entry
// whose frozen snapshot may change because of it.
//
// An entry for knowledge version K is stale when the new revision is visible
// at K (rev.Rev <= K) and either
//   - the edge is in the entry's dependency set (it was known at K and was
//     examined while answering — on the cached path or not), or
//   - the edge had no revision visible at K so far (a backfilled revision
//     makes a previously unknown edge appear in the frozen graph).
//
// Revisions with Rev > K cannot alter the frozen graph at K and keep the
// entry valid.
func (c *PathCache) AddRevision(id EdgeID, rev Revision) error {
	ve, ok := c.vg.byID[id]
	if !ok {
		return fmt.Errorf("unknown edge %d", id)
	}
	if err := validateRevision(rev); err != nil {
		return err
	}
	// Evaluate staleness against the pre-revision state.
	for q, e := range c.entries {
		if rev.Rev > q.Knowledge {
			continue
		}
		if e.readSet[id] || !c.vg.hasRevisionLE(id, q.Knowledge) {
			delete(c.entries, q)
		}
	}
	return c.vg.insertRevision(ve, rev)
}

// Size reports the number of cached entries.
func (c *PathCache) Size() int {
	return len(c.entries)
}
