package trail

import (
	"errors"
	"fmt"
	"sort"
)

// ErrStalePlan is returned when a published plan no longer matches the
// current edge-set version, i.e. new edges were imported after the plan
// was published.
var ErrStalePlan = errors.New("trail: plan was published against a stale edge-set version")

// Plan is a published route over the not-yet-walked edges. Version ties
// the plan to the edge set it was computed from; importing new edges
// invalidates it.
type Plan struct {
	Version int
	EdgeIDs []string
}

// Checkpoint captures the walked-edge identities needed to resume route
// planning after a restart.
type Checkpoint struct {
	Version       int
	WalkedEdgeIDs []string
}

// Planner tracks an edge set that grows in batches plus an immutable
// walked prefix supplied by the business side. The remaining route must
// continue from the current node and cover every un-walked edge exactly
// once; walked edges are never re-used.
type Planner struct {
	start   string
	edges   map[string]Edge
	walked  []string
	current string
	version int
}

// NewPlanner builds a Planner from the full edge set known so far and the
// walked prefix. The prefix must be a chain departing from start and every
// walked edge must exist in the edge set.
func NewPlanner(start string, edges []Edge, walked []string) (*Planner, error) {
	p := &Planner{start: start, edges: make(map[string]Edge, len(edges)), version: 1}
	for _, e := range edges {
		if _, dup := p.edges[e.ID]; dup {
			return nil, &DuplicateEdgeIDError{ID: e.ID}
		}
		p.edges[e.ID] = e
	}
	if err := p.applyWalked(walked); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Planner) applyWalked(walked []string) error {
	seen := make(map[string]bool, len(walked))
	cur := p.start
	for _, id := range walked {
		e, ok := p.edges[id]
		if !ok {
			return fmt.Errorf("trail: walked edge %q is not in the edge set", id)
		}
		if seen[id] {
			return fmt.Errorf("trail: walked edge %q appears twice in the prefix", id)
		}
		if e.From != cur {
			return fmt.Errorf("trail: walked prefix broken at edge %q: departs from %q, expected %q", id, e.From, cur)
		}
		seen[id] = true
		cur = e.To
	}
	p.walked = append([]string(nil), walked...)
	p.current = cur
	return nil
}

// ImportBatch adds a newly arrived batch of edges and bumps the edge-set
// version, invalidating every plan published so far.
func (p *Planner) ImportBatch(batch []Edge) error {
	fresh := make(map[string]bool, len(batch))
	for _, e := range batch {
		if _, dup := p.edges[e.ID]; dup {
			return &DuplicateEdgeIDError{ID: e.ID}
		}
		if fresh[e.ID] {
			return &DuplicateEdgeIDError{ID: e.ID}
		}
		fresh[e.ID] = true
	}
	for _, e := range batch {
		p.edges[e.ID] = e
	}
	p.version++
	return nil
}

// PublishRemaining computes the lexicographically smallest route that
// continues from the current node and covers every un-walked edge. The
// plan is stamped with the current edge-set version.
func (p *Planner) PublishRemaining() (Plan, error) {
	route, err := Solve(p.current, p.remainingEdges())
	if err != nil {
		return Plan{}, err
	}
	return Plan{Version: p.version, EdgeIDs: route}, nil
}

// ValidatePlan checks that a previously published plan is still current:
// its version must match the edge-set version and its route must equal the
// optimal remaining route.
func (p *Planner) ValidatePlan(plan Plan) error {
	if plan.Version != p.version {
		return ErrStalePlan
	}
	want, err := Solve(p.current, p.remainingEdges())
	if err != nil {
		return err
	}
	if !equalIDs(plan.EdgeIDs, want) {
		return fmt.Errorf("trail: plan route %v does not match optimal remaining route %v", plan.EdgeIDs, want)
	}
	return nil
}

// Checkpoint saves the walked-edge identities and the edge-set version for
// later recovery.
func (p *Planner) Checkpoint() Checkpoint {
	return Checkpoint{Version: p.version, WalkedEdgeIDs: append([]string(nil), p.walked...)}
}

// Recover rebuilds a Planner from a checkpoint. The walked prefix is
// re-validated against the supplied edge set, and the prefix constraint is
// verified by full permutation search (for instances within the exhaustive
// limit) to confirm the walked edges leave a completable route.
func Recover(start string, edges []Edge, cp Checkpoint) (*Planner, error) {
	p, err := NewPlanner(start, edges, cp.WalkedEdgeIDs)
	if err != nil {
		return nil, err
	}
	p.version = cp.Version
	if len(edges) <= exhaustiveLimit {
		if _, ok := exhaustiveRoute(start, edges, cp.WalkedEdgeIDs); !ok {
			return nil, fmt.Errorf("trail: checkpoint prefix admits no completion over the full edge set")
		}
	}
	if _, err := Solve(p.current, p.remainingEdges()); err != nil {
		return nil, err
	}
	return p, nil
}

// Version returns the current edge-set version.
func (p *Planner) Version() int { return p.version }

// Current returns the node the remaining route must depart from.
func (p *Planner) Current() string { return p.current }

// Walked returns a copy of the immutable walked prefix.
func (p *Planner) Walked() []string { return append([]string(nil), p.walked...) }

func (p *Planner) remainingEdges() []Edge {
	walked := make(map[string]bool, len(p.walked))
	for _, id := range p.walked {
		walked[id] = true
	}
	rem := make([]Edge, 0, len(p.edges)-len(p.walked))
	for id, e := range p.edges {
		if !walked[id] {
			rem = append(rem, e)
		}
	}
	sort.Slice(rem, func(i, j int) bool { return rem[i].ID < rem[j].ID })
	return rem
}
