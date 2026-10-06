package pdp

import "fmt"

// ReqStatus is the lifecycle state of one request.
type ReqStatus int

const (
	StatusPending   ReqStatus = iota // created, not yet picked up
	StatusOnboard                    // picked up, heading to its delivery point
	StatusReturning                  // cancelled while onboard, must go to the return point
	StatusDelivered                  // delivered to the customer
	StatusReturned                   // cancelled onboard and dropped at the return point
	StatusVoided                     // cancelled before pickup, removed from the plan
)

func (s ReqStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusOnboard:
		return "onboard"
	case StatusReturning:
		return "returning"
	case StatusDelivered:
		return "delivered"
	case StatusReturned:
		return "returned"
	case StatusVoided:
		return "voided"
	}
	return fmt.Sprintf("ReqStatus(%d)", int(s))
}

// JournalEntry is one recorded snapshot of the whole system: every
// request's status plus the load evidence (position, load, cost) taken
// right after an action was applied.
type JournalEntry struct {
	Seq       int               // monotonically increasing sequence number
	Action    string            // what happened, e.g. "plan", "advance 2", "cancel 3"
	Pos       int               // vehicle position after the action
	Load      int               // vehicle load after the action
	CostSoFar int               // travel cost of the fixed executed prefix
	Status    map[int]ReqStatus // per-request status snapshot, keyed by request id
}

// Simulator drives one vehicle through its route while cancellation events
// arrive online. The executed prefix of the route is fixed forever; after
// every event the optimal remaining route is recomputed from the current
// position, load and request states.
//
// Cancellation semantics:
//   - pending request: voided, both legs disappear from the plan;
//   - onboard request: must be dropped at the given return point and keeps
//     occupying capacity until then;
//   - already delivered/returned/voided: no effect. In particular a
//     cancellation arriving at the same moment as the delivery finds the
//     delivery already inside the fixed prefix and is ignored.
type Simulator struct {
	in      Instance
	reqs    []Request
	index   map[int]int // request id -> position in reqs
	status  []ReqStatus
	retPt   []int // return point per request, valid while StatusReturning
	pos     int
	load    int
	cost    int
	hist    []Stop
	plan    Solution
	journal []JournalEntry
}

// NewSimulator validates the problem, computes the initial optimal route
// and records the first journal entry.
func NewSimulator(in Instance, reqs []Request) (*Simulator, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if err := in.validateRequests(reqs); err != nil {
		return nil, err
	}
	s := &Simulator{
		in:     in,
		reqs:   append([]Request{}, reqs...),
		index:  make(map[int]int, len(reqs)),
		status: make([]ReqStatus, len(reqs)),
		retPt:  make([]int, len(reqs)),
		pos:    in.Depot,
	}
	for i, r := range reqs {
		s.index[r.ID] = i
		s.retPt[i] = -1
	}
	s.replan()
	s.record("plan")
	return s, nil
}

// Plan returns the current optimal remaining route (a copy).
func (s *Simulator) Plan() Solution { return copySolution(s.plan) }

// Pos returns the vehicle's current point id.
func (s *Simulator) Pos() int { return s.pos }

// Load returns the vehicle's current load.
func (s *Simulator) Load() int { return s.load }

// CostSoFar returns the travel cost of the fixed executed prefix.
func (s *Simulator) CostSoFar() int { return s.cost }

// Status returns the current status of a request.
func (s *Simulator) Status(reqID int) (ReqStatus, error) {
	i, ok := s.index[reqID]
	if !ok {
		return 0, fmt.Errorf("pdp: unknown request id %d", reqID)
	}
	return s.status[i], nil
}

// History returns the fixed executed prefix of stops.
func (s *Simulator) History() []Stop { return append([]Stop{}, s.hist...) }

// Journal returns every recorded state/load snapshot in order.
func (s *Simulator) Journal() []JournalEntry {
	out := make([]JournalEntry, len(s.journal))
	for i, e := range s.journal {
		cp := e
		cp.Status = make(map[int]ReqStatus, len(e.Status))
		for k, v := range e.Status {
			cp.Status[k] = v
		}
		out[i] = cp
	}
	return out
}

// Advance executes the first k stops of the current plan, fixing them into
// the prefix, then recomputes the optimal remaining route.
func (s *Simulator) Advance(k int) error {
	if k < 0 || k > len(s.plan.Stops) {
		return fmt.Errorf("pdp: cannot advance %d stops with %d planned", k, len(s.plan.Stops))
	}
	for _, st := range s.plan.Stops[:k] {
		s.cost += s.in.Dist[s.pos][st.Node]
		s.pos = st.Node
		s.load = st.LoadAfter
		i := s.index[st.ReqID]
		switch st.Kind {
		case PickupEvent:
			s.status[i] = StatusOnboard
		case DeliveryEvent:
			s.status[i] = StatusDelivered
		case ReturnEvent:
			s.status[i] = StatusReturned
		}
		s.hist = append(s.hist, st)
	}
	s.replan()
	s.record(fmt.Sprintf("advance %d", k))
	return nil
}

// Cancel applies a cancellation event for reqID. returnPoint is only used
// when the request is currently onboard: the request must then be dropped
// there and keeps occupying capacity until the drop happens.
func (s *Simulator) Cancel(reqID, returnPoint int) error {
	i, ok := s.index[reqID]
	if !ok {
		return fmt.Errorf("pdp: unknown request id %d", reqID)
	}
	switch s.status[i] {
	case StatusPending:
		s.status[i] = StatusVoided
	case StatusOnboard:
		if returnPoint < 0 || returnPoint >= len(s.in.Dist) {
			return fmt.Errorf("pdp: return point %d out of range", returnPoint)
		}
		s.status[i] = StatusReturning
		s.retPt[i] = returnPoint
	default:
		// Delivered, returned or already voided: the cancellation has no
		// effect. A cancellation at the exact delivery moment lands here
		// because the delivery is already part of the fixed prefix.
	}
	s.replan()
	s.record(fmt.Sprintf("cancel %d", reqID))
	return nil
}

// replan rebuilds the remaining task set from the request states and solves
// it from the current position and load.
func (s *Simulator) replan() {
	var tasks []task
	for i, r := range s.reqs {
		switch s.status[i] {
		case StatusPending:
			tasks = append(tasks, task{reqID: r.ID, pickup: r.Pickup, drop: r.Delivery, dropKind: DeliveryEvent, weight: r.Weight})
		case StatusOnboard:
			tasks = append(tasks, task{reqID: r.ID, pickup: -1, drop: r.Delivery, dropKind: DeliveryEvent, weight: r.Weight})
		case StatusReturning:
			tasks = append(tasks, task{reqID: r.ID, pickup: -1, drop: s.retPt[i], dropKind: ReturnEvent, weight: r.Weight})
		}
	}
	s.plan = solveTasks(s.in, tasks, s.pos, s.load)
}

func (s *Simulator) record(action string) {
	snap := make(map[int]ReqStatus, len(s.reqs))
	for i, r := range s.reqs {
		snap[r.ID] = s.status[i]
	}
	s.journal = append(s.journal, JournalEntry{
		Seq:       len(s.journal),
		Action:    action,
		Pos:       s.pos,
		Load:      s.load,
		CostSoFar: s.cost,
		Status:    snap,
	})
}
