package roadgraph

import (
	"fmt"
	"sort"
)

// Revision is one versioned fact about an edge: a cost or a closure, valid
// over the half-open interval [Start, End). Rev is the revision number;
// a query with knowledge version K sees exactly the revisions with Rev <= K.
type Revision struct {
	Rev    int64
	Start  int64
	End    int64
	Cost   int64 // non-negative; ignored when Closed is true
	Closed bool  // closure event: the edge is unusable inside the interval
}

// VersionedEdge is an edge together with its revision history, kept sorted
// by revision number.
type VersionedEdge struct {
	ID       EdgeID
	From, To int
	Revs     []Revision
}

// VersionedGraph is a directed multigraph whose edge costs and closures are
// revisioned and time-bounded.
type VersionedGraph struct {
	NumNodes int
	adj      [][]*VersionedEdge
	byID     map[EdgeID]*VersionedEdge
}

// NewVersionedGraph returns an empty versioned graph.
func NewVersionedGraph(numNodes int) *VersionedGraph {
	if numNodes < 0 {
		numNodes = 0
	}
	return &VersionedGraph{
		NumNodes: numNodes,
		adj:      make([][]*VersionedEdge, numNodes),
		byID:     make(map[EdgeID]*VersionedEdge),
	}
}

func validateRevision(rev Revision) error {
	if rev.Cost < 0 {
		return fmt.Errorf("revision %d: negative cost %d", rev.Rev, rev.Cost)
	}
	if rev.End < rev.Start {
		return fmt.Errorf("revision %d: empty interval [%d,%d)", rev.Rev, rev.Start, rev.End)
	}
	return nil
}

func (vg *VersionedGraph) insertRevision(ve *VersionedEdge, rev Revision) error {
	i := sort.Search(len(ve.Revs), func(i int) bool { return ve.Revs[i].Rev >= rev.Rev })
	if i < len(ve.Revs) && ve.Revs[i].Rev == rev.Rev {
		return fmt.Errorf("edge %d: duplicate revision %d", ve.ID, rev.Rev)
	}
	ve.Revs = append(ve.Revs, Revision{})
	copy(ve.Revs[i+1:], ve.Revs[i:])
	ve.Revs[i] = rev
	return nil
}

// AddEdge adds an edge with its initial revision history (may be empty;
// revisions can be supplied later via AddRevision).
func (vg *VersionedGraph) AddEdge(id EdgeID, from, to int, revs ...Revision) error {
	if from < 0 || from >= vg.NumNodes || to < 0 || to >= vg.NumNodes {
		return fmt.Errorf("edge %d: endpoint out of range", id)
	}
	if _, dup := vg.byID[id]; dup {
		return fmt.Errorf("edge %d: duplicate id", id)
	}
	ve := &VersionedEdge{ID: id, From: from, To: to}
	for _, r := range revs {
		if err := validateRevision(r); err != nil {
			return err
		}
		if err := vg.insertRevision(ve, r); err != nil {
			return err
		}
	}
	vg.byID[id] = ve
	vg.adj[from] = append(vg.adj[from], ve)
	return nil
}

// AddRevision applies a local revision to one edge.
func (vg *VersionedGraph) AddRevision(id EdgeID, rev Revision) error {
	ve, ok := vg.byID[id]
	if !ok {
		return fmt.Errorf("unknown edge %d", id)
	}
	if err := validateRevision(rev); err != nil {
		return err
	}
	return vg.insertRevision(ve, rev)
}

// effectiveRevision returns the latest revision known at the given knowledge
// version, or false when the edge is not yet known at all.
func (ve *VersionedEdge) effectiveRevision(knowledge int64) (Revision, bool) {
	idx := sort.Search(len(ve.Revs), func(i int) bool { return ve.Revs[i].Rev > knowledge }) - 1
	if idx < 0 {
		return Revision{}, false
	}
	return ve.Revs[idx], true
}

func (vg *VersionedGraph) hasRevisionLE(id EdgeID, knowledge int64) bool {
	ve, ok := vg.byID[id]
	if !ok {
		return false
	}
	_, known := ve.effectiveRevision(knowledge)
	return known
}

// Freeze materializes the plain graph seen by a query with the given
// knowledge version and departure time. Every edge of any path is judged
// against this single frozen snapshot: an edge is present iff its latest
// revision with Rev <= knowledge covers depart and is not a closure.
//
// It also returns the considered set: every edge that had at least one
// revision with Rev <= knowledge and therefore participated in the decision,
// whether or not it ended up active.
func (vg *VersionedGraph) Freeze(knowledge, depart int64) (*Graph, []EdgeID) {
	g := NewGraph(vg.NumNodes)
	considered := make([]EdgeID, 0, len(vg.byID))
	for _, ve := range vg.byID {
		rev, known := ve.effectiveRevision(knowledge)
		if !known {
			continue
		}
		considered = append(considered, ve.ID)
		if rev.Closed || depart < rev.Start || depart >= rev.End {
			continue
		}
		// Cannot fail: endpoints and cost were validated on insertion.
		_ = g.AddEdge(ve.ID, ve.From, ve.To, rev.Cost)
	}
	return g, considered
}

// Query identifies a shortest-path request: a fixed knowledge version and
// departure time, plus endpoints.
type Query struct {
	Knowledge int64
	Depart    int64
	Source    int
	Target    int
}

// QueryResult is the answer to a Query. Distance is -1 when Reachable is
// false. Path is one shortest witness as edge IDs of the frozen graph.
type QueryResult struct {
	Reachable bool
	Distance  int64
	Path      []EdgeID
}

func computeQueryResult(g *Graph, source, target int) QueryResult {
	sr := g.ShortestPaths(source, nil)
	path, ok := sr.PathTo(target)
	if !ok {
		return QueryResult{Reachable: false, Distance: -1}
	}
	return QueryResult{Reachable: true, Distance: sr.Dist[target], Path: path}
}

// Query answers q by a full recompute: freeze the graph, then run Dijkstra.
// It never touches any cache and serves as the ground-truth oracle.
func (vg *VersionedGraph) Query(q Query) QueryResult {
	frozen, _ := vg.Freeze(q.Knowledge, q.Depart)
	return computeQueryResult(frozen, q.Source, q.Target)
}
