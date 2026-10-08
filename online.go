package pdp

import (
	"errors"
	"fmt"
)

// ItemStatus is the lifecycle state of one request in a Simulator.
type ItemStatus int

const (
	StatusPending   ItemStatus = iota // not picked up yet
	StatusOnBoard                     // picked up, heading to its drop node
	StatusDelivered                   // delivered to the customer
	StatusCancelled                   // cancelled before pickup; fully removed
	StatusReturning                   // cancelled after pickup; must reach its return point
	StatusReturned                    // returned to the return point
)

func (s ItemStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusOnBoard:
		return "on-board"
	case StatusDelivered:
		return "delivered"
	case StatusCancelled:
		return "cancelled"
	case StatusReturning:
		return "returning"
	case StatusReturned:
		return "returned"
	}
	return "unknown"
}

// EventKind classifies a Simulator event.
type EventKind int

const (
	// EventStep executes the next stop of the current optimal plan,
	// appending it to the fixed completed prefix.
	EventStep EventKind = iota
	// EventCancel cancels a request. A pending request is removed
	// entirely; an on-board request must be delivered to the ReturnTo
	// node and keeps occupying capacity until then. Cancelling a
	// delivered, returned, or already cancelled request is an error.
	EventCancel
)

// Event is one online event. Events apply strictly in the order they are
// submitted, which fixes the semantics of simultaneous happenings: a
// cancel submitted before the step that delivers the request still finds
// it on board (it must be returned); a cancel submitted after that step
// finds it delivered and is rejected.
type Event struct {
	Kind     EventKind
	Request  int // request id, for EventCancel
	ReturnTo int // return point node, for EventCancel
}

// Step returns an event executing the next planned stop.
func Step() Event { return Event{Kind: EventStep} }

// Cancel returns an event cancelling request; returnTo is the node an
// on-board item must be brought back to.
func Cancel(request, returnTo int) Event {
	return Event{Kind: EventCancel, Request: request, ReturnTo: returnTo}
}

// ItemRecord is the saved state of one request after an event.
type ItemRecord struct {
	Request  int
	Status   ItemStatus
	ReturnTo int // valid while StatusReturning or StatusReturned
}

// Snapshot is the full evidence saved after an event: every request's
// state, the vehicle position and load, the fixed completed prefix with
// its load trajectory, and the optimal remaining route with its own load
// trajectory.
type Snapshot struct {
	Event     Event
	Items     []ItemRecord
	Pos       int
	Load      int
	Executed  []Stop
	Remaining Route
}

// Simulator drives a Problem through a stream of online events. The
// completed prefix is fixed forever; after every event the optimal
// remaining route is recomputed from the current position and load.
type Simulator struct {
	prob     Problem
	idx      map[int]int
	status   []ItemStatus
	returnTo []int
	pos      int
	load     int
	executed []Stop
	plan     Route
}

// NewSimulator validates p and computes the initial optimal route.
func NewSimulator(p Problem) (*Simulator, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	s := &Simulator{
		prob:     p,
		idx:      make(map[int]int, len(p.Requests)),
		status:   make([]ItemStatus, len(p.Requests)),
		returnTo: make([]int, len(p.Requests)),
		pos:      p.Start,
	}
	for i, r := range p.Requests {
		s.idx[r.ID] = i
	}
	if err := s.replan(); err != nil {
		return nil, err
	}
	return s, nil
}

// Plan returns the current optimal remaining route.
func (s *Simulator) Plan() Route { return s.plan }

// Current returns the snapshot of the current state without applying an
// event.
func (s *Simulator) Current() Snapshot { return s.snapshot(Event{}) }

// Apply processes one event and returns the resulting snapshot with the
// recomputed optimal remaining route.
func (s *Simulator) Apply(ev Event) (Snapshot, error) {
	switch ev.Kind {
	case EventStep:
		if len(s.plan.Stops) == 0 {
			return Snapshot{}, errors.New("pdp: no remaining stops to execute")
		}
		st := s.plan.Stops[0]
		i := s.idx[st.Request]
		s.pos = st.Node
		s.load = st.Load
		switch st.Kind {
		case PickupStop:
			s.status[i] = StatusOnBoard
		case DeliverStop:
			s.status[i] = StatusDelivered
		case ReturnStop:
			s.status[i] = StatusReturned
		}
		s.executed = append(s.executed, st)
	case EventCancel:
		i, ok := s.idx[ev.Request]
		if !ok {
			return Snapshot{}, fmt.Errorf("pdp: unknown request %d", ev.Request)
		}
		if ev.ReturnTo < 0 || ev.ReturnTo >= s.prob.NumNodes {
			return Snapshot{}, fmt.Errorf("pdp: return node %d out of range", ev.ReturnTo)
		}
		switch s.status[i] {
		case StatusPending:
			s.status[i] = StatusCancelled
		case StatusOnBoard:
			s.status[i] = StatusReturning
			s.returnTo[i] = ev.ReturnTo
		default:
			return Snapshot{}, fmt.Errorf("pdp: request %d cannot be cancelled while %s", ev.Request, s.status[i])
		}
	default:
		return Snapshot{}, fmt.Errorf("pdp: unknown event kind %d", ev.Kind)
	}
	if err := s.replan(); err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ev), nil
}

// CrossCheck verifies the current remaining plan against exhaustive
// enumeration of every legal permutation of the remaining events. It
// returns an error if the plan's cost or visited node id sequence differs
// from the brute-force optimum.
func (s *Simulator) CrossCheck() error {
	want, err := bruteLegs(s.prob.Dist, s.prob.Capacity, s.prob.Start, s.pos, s.legs())
	if err != nil {
		return fmt.Errorf("pdp: brute force failed: %v", err)
	}
	if want.TotalDist != s.plan.TotalDist {
		return fmt.Errorf("pdp: plan cost %d, brute-force optimum %d", s.plan.TotalDist, want.TotalDist)
	}
	if !equalSeq(want.Nodes(), s.plan.Nodes()) {
		return fmt.Errorf("pdp: plan visits %v, brute-force optimum visits %v", s.plan.Nodes(), want.Nodes())
	}
	return nil
}

// legs derives the residual tasks from the per-request states.
func (s *Simulator) legs() []Leg {
	var legs []Leg
	for i, r := range s.prob.Requests {
		switch s.status[i] {
		case StatusPending:
			legs = append(legs, Leg{Request: r.ID, Pickup: r.Pickup, Drop: r.Drop, Weight: r.Weight})
		case StatusOnBoard:
			legs = append(legs, Leg{Request: r.ID, Drop: r.Drop, Weight: r.Weight, Picked: true})
		case StatusReturning:
			legs = append(legs, Leg{Request: r.ID, Drop: s.returnTo[i], Weight: r.Weight, Picked: true, Returning: true})
		}
	}
	return legs
}

func (s *Simulator) replan() error {
	rt, err := solveLegs(s.prob.Dist, s.prob.Capacity, s.prob.Start, s.pos, s.legs())
	if err != nil {
		return err
	}
	s.plan = rt
	return nil
}

func (s *Simulator) snapshot(ev Event) Snapshot {
	snap := Snapshot{
		Event:     ev,
		Pos:       s.pos,
		Load:      s.load,
		Executed:  append([]Stop(nil), s.executed...),
		Remaining: s.plan,
	}
	for i, r := range s.prob.Requests {
		snap.Items = append(snap.Items, ItemRecord{
			Request:  r.ID,
			Status:   s.status[i],
			ReturnTo: s.returnTo[i],
		})
	}
	return snap
}
