// Package versioned models a road network whose edge costs and closure
// events carry a valid time interval and a revision number.
//
// A query fixes a knowledge version (revision cutoff) and a departure
// time. The whole query is answered against a single frozen snapshot: every
// edge of a path is judged by the state known at that revision for that
// departure time, never by arrival-time propagation.
//
// The package also provides a path cache that survives local revisions
// when they provably cannot change the answer. The shortestness
// certificate of a cached entry depends on the exact state of the path
// edges AND on the fact that no other edge became cheaper; invalidating
// only by the edges on the path is unsound (an off-path price drop can
// beat the cached path).
package versioned

import (
	"roadnet/graph"
)

// Time is an absolute time coordinate (e.g. seconds since an epoch).
type Time int64

// Revision identifies a knowledge version. Revision 0 is the base state;
// every call to Revise creates the next revision.
type Revision int64

// CostChange overrides the cost of an edge during [From, To).
type CostChange struct {
	Edge int
	From Time
	To   Time
	Cost int64
}

// ClosureEvent closes an edge during [From, To).
type ClosureEvent struct {
	Edge int
	From Time
	To   Time
}

type costRecord struct {
	CostChange
	rev Revision
}

type closureRecord struct {
	ClosureEvent
	rev Revision
}

// edgeState is the frozen state of one edge at a query point.
type edgeState struct {
	cost   int64
	closed bool
}

// KnowledgeBase stores the base topology plus all revisioned records.
// Records are append-only, so a frozen snapshot at (revision, time) is
// immutable and reproducible.
type KnowledgeBase struct {
	numNodes int
	edges    []graph.Edge // parallel edges distinguished by ID
	baseCost []int64
	costs    []costRecord
	closures []closureRecord
	rev      Revision
	listener func(newRev Revision, changedEdges []int)
}

// NewKnowledgeBase returns an empty base with numNodes nodes.
func NewKnowledgeBase(numNodes int) *KnowledgeBase {
	return &KnowledgeBase{numNodes: numNodes}
}

// AddEdge adds a directed edge with its base cost (revision 0, valid for
// all times) and returns its unique ID. Parallel edges each get their own
// ID. baseCost must be >= 0.
//
// The topology is static: all edges must be added before the first Revise,
// so that a frozen snapshot at any past revision stays immutable.
func (kb *KnowledgeBase) AddEdge(from, to int, baseCost int64) int {
	if baseCost < 0 {
		panic("versioned: negative base cost")
	}
	if kb.rev != 0 {
		panic("versioned: topology is fixed once revisions begin")
	}
	id := len(kb.edges)
	kb.edges = append(kb.edges, graph.Edge{ID: id, From: from, To: to, Weight: baseCost})
	kb.baseCost = append(kb.baseCost, baseCost)
	return id
}

// NumEdges returns the number of edges in the base topology.
func (kb *KnowledgeBase) NumEdges() int { return len(kb.edges) }

// CurrentRevision returns the latest revision.
func (kb *KnowledgeBase) CurrentRevision() Revision { return kb.rev }

// Revise applies a batch of local cost changes and closure events as a
// single new revision and returns its number. The attached cache (if any)
// is notified and invalidates dependent entries.
func (kb *KnowledgeBase) Revise(costs []CostChange, closures []ClosureEvent) Revision {
	kb.rev++
	changed := make(map[int]bool)
	for _, c := range costs {
		if c.Edge < 0 || c.Edge >= len(kb.edges) {
			panic("versioned: cost change for unknown edge")
		}
		if c.Cost < 0 {
			panic("versioned: negative cost")
		}
		kb.costs = append(kb.costs, costRecord{CostChange: c, rev: kb.rev})
		changed[c.Edge] = true
	}
	for _, cl := range closures {
		if cl.Edge < 0 || cl.Edge >= len(kb.edges) {
			panic("versioned: closure for unknown edge")
		}
		kb.closures = append(kb.closures, closureRecord{ClosureEvent: cl, rev: kb.rev})
		changed[cl.Edge] = true
	}
	if kb.listener != nil {
		ids := make([]int, 0, len(changed))
		for id := range changed {
			ids = append(ids, id)
		}
		kb.listener(kb.rev, ids)
	}
	return kb.rev
}

// stateAt returns the frozen state of edge id as known at revision knowRev
// for a departure at time depart: the cost record with the highest revision
// <= knowRev whose interval contains depart (base cost if none), and whether
// a known closure covers depart.
func (kb *KnowledgeBase) stateAt(id int, knowRev Revision, depart Time) edgeState {
	st := edgeState{cost: kb.baseCost[id]}
	var bestRev Revision
	for _, r := range kb.costs {
		if r.Edge == id && r.rev <= knowRev && r.From <= depart && depart < r.To && r.rev > bestRev {
			bestRev = r.rev
			st.cost = r.Cost
		}
	}
	for _, r := range kb.closures {
		if r.Edge == id && r.rev <= knowRev && r.From <= depart && depart < r.To {
			st.closed = true
			break
		}
	}
	return st
}

// Snapshot is a frozen, static view of the network. Every path computed on
// it is judged entirely against this one snapshot.
type Snapshot struct {
	Graph   *graph.Graph
	EdgeIDs []int // frozen edge ID -> base edge ID
}

// FrozenGraph builds the static snapshot as known at revision knowRev for
// a departure at time depart. Closed edges are omitted.
func (kb *KnowledgeBase) FrozenGraph(knowRev Revision, depart Time) Snapshot {
	g := graph.New(kb.numNodes)
	ids := make([]int, 0, len(kb.edges))
	for id := range kb.edges {
		st := kb.stateAt(id, knowRev, depart)
		if st.closed {
			continue
		}
		e := kb.edges[id]
		g.AddEdge(e.From, e.To, st.cost)
		ids = append(ids, id)
	}
	return Snapshot{Graph: g, EdgeIDs: ids}
}

// Answer is the result of a shortest path query.
type Answer struct {
	Dist      int64
	Reachable bool
	Path      []int // base edge IDs in s -> t order
}

// ShortestPathAt answers a query by full recomputation against the frozen
// snapshot at (knowRev, depart). It is the ground truth used to verify the
// cache.
func (kb *KnowledgeBase) ShortestPathAt(knowRev Revision, depart Time, s, t int) Answer {
	snap := kb.FrozenGraph(knowRev, depart)
	res := snap.Graph.ShortestPath(s, t, graph.NoEdge)
	ans := Answer{Dist: res.Dist, Reachable: res.Reachable}
	for _, fid := range res.Path {
		ans.Path = append(ans.Path, snap.EdgeIDs[fid])
	}
	return ans
}

// Query identifies a cached shortest path request at the current knowledge
// revision.
type Query struct {
	S      int
	T      int
	Depart Time
}

type entry struct {
	knowRev Revision
	ans     Answer
	// pathState records the frozen state of every edge on the cached path
	// at (knowRev, depart). An empty path (unreachable or s == t) has no
	// path dependencies; shortestness then rests entirely on the
	// "nothing got cheaper" certificate.
	pathState map[int]edgeState
}

// Cache memoizes shortest path answers per Query at the current revision.
// Entries survive a revision only when the revision provably cannot change
// the answer:
//
//   - a change to an edge on the cached path (any direction) evicts;
//   - a change to any other edge evicts only if it makes that edge cheaper
//     than what the entry froze (lower cost, or a closure lifted) — a
//     cached shortest path stays shortest when off-path edges only get
//     more expensive or closed.
//
// Checking only the path edges would be unsound: an off-path price drop
// can produce a path shorter than the cached one.
type Cache struct {
	kb        *KnowledgeBase
	entries   map[Query]*entry
	Hits      int
	Misses    int
	Evictions int
}

// NewCache returns a cache bound to kb. The cache is automatically
// notified of every subsequent Revise on kb.
func NewCache(kb *KnowledgeBase) *Cache {
	c := &Cache{kb: kb, entries: make(map[Query]*entry)}
	kb.listener = func(newRev Revision, changedEdges []int) {
		c.onRevision(changedEdges)
	}
	return c
}

// Len returns the number of cached entries.
func (c *Cache) Len() int { return len(c.entries) }

// Query returns the shortest path answer at the current revision, using
// the cache when a provably valid entry exists.
func (c *Cache) Query(q Query) Answer {
	if e, ok := c.entries[q]; ok {
		c.Hits++
		return e.ans
	}
	c.Misses++
	ans := c.kb.ShortestPathAt(c.kb.CurrentRevision(), q.Depart, q.S, q.T)
	e := &entry{knowRev: c.kb.CurrentRevision(), ans: ans, pathState: make(map[int]edgeState)}
	for _, id := range ans.Path {
		e.pathState[id] = c.kb.stateAt(id, e.knowRev, q.Depart)
	}
	c.entries[q] = e
	return ans
}

// onRevision evicts every entry whose correctness the changed edges can
// affect. changedEdges are the base edge IDs touched by the revision.
func (c *Cache) onRevision(changedEdges []int) {
	for q, e := range c.entries {
		if c.entryInvalidated(q, e, changedEdges) {
			delete(c.entries, q)
			c.Evictions++
		}
	}
}

func (c *Cache) entryInvalidated(q Query, e *entry, changedEdges []int) bool {
	for _, id := range changedEdges {
		old := c.kb.stateAt(id, e.knowRev, q.Depart)
		now := c.kb.stateAt(id, c.kb.CurrentRevision(), q.Depart)
		if _, onPath := e.pathState[id]; onPath {
			// Any change to a path edge (cost either way, closure
			// opened or lifted) can change the answer.
			if old != now {
				return true
			}
			continue
		}
		// Off-path edge: only a strictly cheaper state breaks the
		// shortestness certificate of the cached path.
		if cheaper(now, old) {
			return true
		}
	}
	return false
}

// cheaper reports whether state n is strictly more attractive than state o
// for a traveller: a closure was lifted, or the cost of an open edge
// decreased.
func cheaper(n, o edgeState) bool {
	if o.closed {
		return !n.closed
	}
	return !n.closed && n.cost < o.cost
}
