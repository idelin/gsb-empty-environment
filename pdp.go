// Package pdp solves small capacitated pickup-and-delivery problems for a
// single vehicle that starts and ends at the same depot. Distances are
// non-negative and given as a complete matrix. Each request must be picked
// up before it is delivered, deliveries release capacity, and ties between
// equal-cost routes are broken by the lexicographic sequence of visited
// node ids.
package pdp

import (
	"errors"
	"fmt"
)

// MaxRequests is the maximum number of pickup-delivery pairs supported.
const MaxRequests = 6

// ErrInfeasible reports that no capacity-respecting route exists.
var ErrInfeasible = errors.New("pdp: no feasible route")

// Request is one pickup-delivery pair. Weight units are shared with
// Problem.Capacity.
type Request struct {
	ID     int
	Pickup int
	Drop   int
	Weight int
}

// Problem is a static pickup-and-delivery instance.
type Problem struct {
	NumNodes int
	Dist     [][]int // complete, non-negative, NumNodes x NumNodes
	Capacity int
	Start    int // depot; the vehicle starts and ends here
	Requests []Request
}

// StopKind classifies a visit.
type StopKind int

const (
	PickupStop StopKind = iota
	DeliverStop
	ReturnStop // delivery to a return point after an online cancellation
)

func (k StopKind) String() string {
	switch k {
	case PickupStop:
		return "pickup"
	case DeliverStop:
		return "deliver"
	case ReturnStop:
		return "return"
	}
	return "unknown"
}

// Stop is one visit of a route. Load is the vehicle load right after the
// stop is performed; the sequence of Load values is the load trajectory.
type Stop struct {
	Node    int
	Request int
	Kind    StopKind
	Load    int
}

// Route is a full plan: an ordered list of stops plus the final leg back
// to the depot. TotalDist includes that final leg.
type Route struct {
	Start     int
	Stops     []Stop
	TotalDist int
}

// Nodes returns the visited node id sequence (used for tie-breaking).
func (r Route) Nodes() []int {
	nodes := make([]int, len(r.Stops))
	for i, s := range r.Stops {
		nodes[i] = s.Node
	}
	return nodes
}

// Loads returns the load trajectory: load after every stop.
func (r Route) Loads() []int {
	loads := make([]int, len(r.Stops))
	for i, s := range r.Stops {
		loads[i] = s.Load
	}
	return loads
}

// Leg is the residual task of one request. It generalises Request to
// partially executed states: Picked legs are already on the vehicle (their
// Pickup node is ignored) and Returning legs head to a return point.
type Leg struct {
	Request   int
	Pickup    int
	Drop      int
	Weight    int
	Picked    bool
	Returning bool
}

func (p Problem) validate() error {
	if p.NumNodes <= 0 {
		return errors.New("pdp: NumNodes must be positive")
	}
	if len(p.Dist) != p.NumNodes {
		return fmt.Errorf("pdp: Dist has %d rows, want %d", len(p.Dist), p.NumNodes)
	}
	for i, row := range p.Dist {
		if len(row) != p.NumNodes {
			return fmt.Errorf("pdp: Dist row %d has %d columns, want %d", i, len(row), p.NumNodes)
		}
		for j, d := range row {
			if d < 0 {
				return fmt.Errorf("pdp: negative distance at (%d,%d)", i, j)
			}
		}
	}
	if p.Capacity < 0 {
		return errors.New("pdp: negative capacity")
	}
	if p.Start < 0 || p.Start >= p.NumNodes {
		return fmt.Errorf("pdp: start node %d out of range", p.Start)
	}
	if len(p.Requests) > MaxRequests {
		return fmt.Errorf("pdp: %d requests exceed the maximum of %d", len(p.Requests), MaxRequests)
	}
	seen := make(map[int]bool, len(p.Requests))
	for _, r := range p.Requests {
		if seen[r.ID] {
			return fmt.Errorf("pdp: duplicate request id %d", r.ID)
		}
		seen[r.ID] = true
		if r.Weight < 0 {
			return fmt.Errorf("pdp: negative weight for request %d", r.ID)
		}
		for _, n := range []int{r.Pickup, r.Drop} {
			if n < 0 || n >= p.NumNodes {
				return fmt.Errorf("pdp: node %d of request %d out of range", n, r.ID)
			}
		}
	}
	return nil
}

// Solve computes the shortest feasible route for p. Among equal-cost
// routes it returns the one with the lexicographically smallest visited
// node id sequence. The joint route is optimised as a whole; requests are
// never split into independent shortest paths.
func Solve(p Problem) (Route, error) {
	if err := p.validate(); err != nil {
		return Route{}, err
	}
	legs := make([]Leg, len(p.Requests))
	for i, r := range p.Requests {
		legs[i] = Leg{Request: r.ID, Pickup: r.Pickup, Drop: r.Drop, Weight: r.Weight}
	}
	return solveLegs(p.Dist, p.Capacity, p.Start, p.Start, legs)
}

// action is one chosen stop in the DP reconstruction.
type action struct {
	leg  int
	kind StopKind
}

type dpResult struct {
	ok   bool
	cost int
	seq  []int    // visited node ids from this state on
	acts []action // parallel to seq
}

type dpKey struct {
	picked, done, pos int
}

type dpSolver struct {
	dist     [][]int
	capacity int
	start    int
	legs     []Leg
	memo     map[dpKey]dpResult
}

func (s *dpSolver) loadOf(picked, done int) int {
	load := 0
	for i, l := range s.legs {
		if picked&(1<<uint(i)) != 0 && done&(1<<uint(i)) == 0 {
			load += l.Weight
		}
	}
	return load
}

// solve returns the optimal (cost, node sequence, actions) to finish all
// legs from the given state and drive back to the depot.
func (s *dpSolver) solve(picked, done, pos int) dpResult {
	key := dpKey{picked, done, pos}
	if r, ok := s.memo[key]; ok {
		return r
	}
	all := (1 << uint(len(s.legs))) - 1
	if done == all {
		r := dpResult{ok: true, cost: s.dist[pos][s.start]}
		s.memo[key] = r
		return r
	}
	load := s.loadOf(picked, done)
	best := dpResult{}
	for i, leg := range s.legs {
		bit := 1 << uint(i)
		if picked&bit == 0 {
			if load+leg.Weight > s.capacity {
				continue
			}
			sub := s.solve(picked|bit, done, leg.Pickup)
			if !sub.ok {
				continue
			}
			best = betterResult(best, dpResult{
				ok:   true,
				cost: s.dist[pos][leg.Pickup] + sub.cost,
				seq:  append([]int{leg.Pickup}, sub.seq...),
				acts: append([]action{{leg: i, kind: PickupStop}}, sub.acts...),
			})
		} else if done&bit == 0 {
			kind := DeliverStop
			if leg.Returning {
				kind = ReturnStop
			}
			sub := s.solve(picked, done|bit, leg.Drop)
			if !sub.ok {
				continue
			}
			best = betterResult(best, dpResult{
				ok:   true,
				cost: s.dist[pos][leg.Drop] + sub.cost,
				seq:  append([]int{leg.Drop}, sub.seq...),
				acts: append([]action{{leg: i, kind: kind}}, sub.acts...),
			})
		}
	}
	s.memo[key] = best
	return best
}

// betterResult picks the cheaper result, breaking ties by the
// lexicographic visited node id sequence.
func betterResult(a, b dpResult) dpResult {
	if !a.ok {
		return b
	}
	if !b.ok {
		return a
	}
	if a.cost != b.cost {
		if a.cost < b.cost {
			return a
		}
		return b
	}
	if lessSeq(a.seq, b.seq) {
		return a
	}
	return b
}

func lessSeq(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func equalSeq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// solveLegs is the shared core behind Solve and the online simulator: it
// optimally finishes the given legs starting at pos, then returns to
// start. Legs already picked contribute their weight to the initial load.
func solveLegs(dist [][]int, capacity, start, pos int, legs []Leg) (Route, error) {
	load := 0
	for _, l := range legs {
		if l.Weight < 0 {
			return Route{}, fmt.Errorf("pdp: negative weight for request %d", l.Request)
		}
		if l.Picked {
			load += l.Weight
		}
	}
	if load > capacity {
		return Route{}, ErrInfeasible
	}
	s := &dpSolver{dist: dist, capacity: capacity, start: start, legs: legs, memo: make(map[dpKey]dpResult)}
	picked := 0
	for i, l := range legs {
		if l.Picked {
			picked |= 1 << uint(i)
		}
	}
	res := s.solve(picked, 0, pos)
	if !res.ok {
		return Route{}, ErrInfeasible
	}
	return buildRoute(start, legs, res), nil
}

func buildRoute(start int, legs []Leg, res dpResult) Route {
	load := 0
	for _, l := range legs {
		if l.Picked {
			load += l.Weight
		}
	}
	rt := Route{Start: start, TotalDist: res.cost}
	for i, a := range res.acts {
		leg := legs[a.leg]
		if a.kind == PickupStop {
			load += leg.Weight
		} else {
			load -= leg.Weight
		}
		rt.Stops = append(rt.Stops, Stop{
			Node:    res.seq[i],
			Request: leg.Request,
			Kind:    a.kind,
			Load:    load,
		})
	}
	return rt
}
