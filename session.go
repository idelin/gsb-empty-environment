package traj

import "errors"

// Session tracks a business-mandated walked prefix over a Graph. The
// prefix is immutable: walked edges keep their identity, can never be
// re-walked, and the remaining route must continue from the current
// point covering every unwalked edge.
type Session struct {
	g        *Graph
	walked   []Edge
	walkedID map[string]bool
	current  string
}

var (
	// ErrUnknownEdge names an edge ID absent from the graph.
	ErrUnknownEdge = errors.New("traj: unknown edge ID")
	// ErrEdgeRewalked means a prefix tried to reuse a walked edge.
	ErrEdgeRewalked = errors.New("traj: edge already walked")
	// ErrPrefixBroken means the next prefix edge does not depart from
	// the current point.
	ErrPrefixBroken = errors.New("traj: prefix edge does not continue the walk")
	// ErrVersionMismatch means a checkpoint was taken against a
	// different edge-set version.
	ErrVersionMismatch = errors.New("traj: checkpoint version does not match graph")
)

// NewSession starts a session at the graph's start point.
func NewSession(g *Graph) *Session {
	return &Session{g: g, walkedID: make(map[string]bool), current: g.Start()}
}

// WalkPrefix applies business-given walked edges in order. Each edge
// must exist, must not have been walked before, and must depart from
// the current point. The walk is append-only: applied edges are never
// modified or removed.
func (s *Session) WalkPrefix(ids []string) error {
	for _, id := range ids {
		e, ok := s.g.Edge(id)
		if !ok {
			return newError(ErrUnknownEdge, "traj: unknown edge ID %q", id)
		}
		if s.walkedID[id] {
			return newError(ErrEdgeRewalked, "traj: edge %q already walked", id)
		}
		if e.From != s.current {
			return newError(ErrPrefixBroken,
				"traj: edge %q departs %q, current point is %q", id, e.From, s.current)
		}
		s.walked = append(s.walked, e)
		s.walkedID[id] = true
		s.current = e.To
	}
	return nil
}

// Current is the point the walk has reached.
func (s *Session) Current() string { return s.current }

// Walked returns the walked prefix in order.
func (s *Session) Walked() []Edge { return append([]Edge(nil), s.walked...) }

// Remaining returns the unwalked edges in import order.
func (s *Session) Remaining() []Edge {
	var out []Edge
	for _, e := range s.g.Edges() {
		if !s.walkedID[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

// RemainingRoute computes the lexicographically smallest route that
// continues from the current point and covers every unwalked edge
// exactly once. Walked edges are never reused.
func (s *Session) RemainingRoute() ([]Edge, *Diagnosis) {
	return FindRoute(s.Remaining(), s.current)
}

// PublishRemaining computes the remaining route and tags it with the
// current edge-set version. Importing a new batch afterwards makes the
// route stale (see Graph.CheckRoute).
func (s *Session) PublishRemaining() (*Route, *Diagnosis) {
	route, diag := s.RemainingRoute()
	if diag != nil {
		return nil, diag
	}
	return &Route{
		Version: s.g.Version(),
		From:    s.current,
		EdgeIDs: RouteIDs(route),
	}, nil
}

// Checkpoint captures the walked edge identities for crash recovery.
type Checkpoint struct {
	Version   int
	WalkedIDs []string
}

// Save records the walked edge identities and the edge-set version.
func (s *Session) Save() Checkpoint {
	return Checkpoint{
		Version:   s.g.Version(),
		WalkedIDs: RouteIDs(s.walked),
	}
}

// Restore rebuilds a session from a checkpoint. The graph must still be
// at the checkpoint's version; the walked edges are re-applied in order
// with full prefix validation.
func Restore(g *Graph, cp Checkpoint) (*Session, error) {
	if cp.Version != g.Version() {
		return nil, newError(ErrVersionMismatch,
			"traj: checkpoint version %d, graph version %d", cp.Version, g.Version())
	}
	s := NewSession(g)
	if err := s.WalkPrefix(cp.WalkedIDs); err != nil {
		return nil, err
	}
	return s, nil
}

// VerifyRemainingExhaustive cross-checks the remaining route against a
// full permutation enumeration of the unwalked edges, honouring the
// prefix constraint (the walk must continue from the current point).
// It is available for remaining sets of at most ExhaustiveLimit edges.
func (s *Session) VerifyRemainingExhaustive() (bool, error) {
	remaining := s.Remaining()
	if len(remaining) > ExhaustiveLimit {
		return false, ErrTooManyEdges
	}
	route, diag := s.RemainingRoute()
	if diag != nil {
		// No route: confirm enumeration agrees none exists.
		_, ok := BruteForceMinRoute(remaining, s.current)
		return !ok, nil
	}
	return VerifyExhaustive(remaining, s.current, route)
}
