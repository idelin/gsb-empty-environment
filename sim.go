package wfqsim

import (
	"container/heap"
	"encoding/json"
)

// Kind classifies events. The numeric value doubles as the processing
// priority within one tick: all events at the same tick are applied in
// Kind order, ties broken by submission sequence number.
type Kind int

const (
	EvCancel       Kind = iota // cancel a queued packet
	EvWithdraw                 // withdraw a flow
	EvWeightFlow               // change a flow's weight
	EvWeightTenant             // change a tenant's weight
	EvPause                    // backpressure: stop link service
	EvResume                   // end backpressure
	EvArrive                   // packet arrival
	EvCheckpoint               // record per-flow served bytes
	EvEnd                      // stop the simulation
)

// Event is a discrete-event-simulation event at an integer tick.
type Event struct {
	Tick     int     `json:"tick"`
	Seq      int     `json:"seq"` // submission order; breaks same-(tick,kind) ties
	Kind     Kind    `json:"kind"`
	Packet   Packet  `json:"packet,omitempty"`   // EvArrive payload
	FlowID   int     `json:"flow,omitempty"`     // EvWithdraw, EvWeightFlow
	TenantID int     `json:"tenant,omitempty"`   // EvWeightTenant
	Weight   float64 `json:"weight,omitempty"`   // EvWeightFlow, EvWeightTenant
	PacketID int     `json:"packetID,omitempty"` // EvCancel
}

// EventQueue is a deterministic min-heap ordered by (Tick, Kind, Seq).
type EventQueue []Event

func (q EventQueue) Len() int { return len(q) }

func (q EventQueue) Less(i, j int) bool {
	a, b := q[i], q[j]
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Seq < b.Seq
}

func (q EventQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *EventQueue) Push(x interface{}) { *q = append(*q, x.(Event)) }
func (q *EventQueue) Pop() interface{} {
	old := *q
	n := len(old)
	e := old[n-1]
	*q = old[:n-1]
	return e
}

// Link is the simulated output link: Rate bytes per tick, pausable
// (backpressure). While paused it transmits nothing and keeps the
// in-service packet's remaining bytes intact.
type Link struct {
	Rate    int  `json:"rate"`
	Paused  bool `json:"paused"`
	Current int  `json:"current"` // packet ID in service, -1 when idle
	Remain  int  `json:"remain"`  // unsent bytes of Current
}

// TraceEntry records one completed packet transmission.
type TraceEntry struct {
	Tick     int `json:"tick"`
	FlowID   int `json:"flow"`
	PacketID int `json:"packet"`
	Bytes    int `json:"bytes"`
}

// Mark is a checkpoint snapshot of per-flow served bytes.
type Mark struct {
	Tick  int            `json:"tick"`
	Bytes map[int]int64  `json:"bytes"`
}

// Sim is the discrete-event simulator. All state is exported so the whole
// simulation (event queue, scheduler, link, trace) can be checkpointed to
// JSON and restored bit-exactly.
type Sim struct {
	Tick   int            `json:"tick"`
	Seq    int            `json:"seq"`
	Sched  *Scheduler     `json:"sched"`
	Link   *Link          `json:"link"`
	Events EventQueue     `json:"events"`
	Trace  []TraceEntry   `json:"trace"`
	Served map[int]int64  `json:"served"` // bytes transmitted per flow
	Marks  []Mark         `json:"marks"`
}

func NewSim(rate int, tenants []Tenant, flows []Flow) *Sim {
	return &Sim{
		Sched:  NewScheduler(tenants, flows),
		Link:   &Link{Rate: rate, Current: -1},
		Served: make(map[int]int64),
	}
}

// Add submits an event, assigning its deterministic sequence number.
func (s *Sim) Add(ev Event) {
	ev.Seq = s.Seq
	s.Seq++
	heap.Push(&s.Events, ev)
}

func (s *Sim) apply(ev Event) {
	switch ev.Kind {
	case EvCancel:
		s.Sched.Cancel(ev.PacketID)
	case EvWithdraw:
		s.Sched.Withdraw(ev.FlowID)
	case EvWeightFlow:
		s.Sched.SetFlowWeight(ev.FlowID, ev.Weight)
	case EvWeightTenant:
		s.Sched.SetTenantWeight(ev.TenantID, ev.Weight)
	case EvPause:
		s.Link.Paused = true
	case EvResume:
		s.Link.Paused = false
	case EvArrive:
		p := ev.Packet
		p.Arrive = s.Tick
		s.Sched.Enqueue(&p)
	case EvCheckpoint:
		m := Mark{Tick: s.Tick, Bytes: make(map[int]int64, len(s.Served))}
		for k, v := range s.Served {
			m.Bytes[k] = v
		}
		s.Marks = append(s.Marks, m)
	}
}

// Step processes every event scheduled at the current tick (in deterministic
// Kind/Seq order) and then advances the link by one tick. It returns false
// when the simulation is finished (EvEnd, or nothing left to do).
func (s *Sim) Step() bool {
	for len(s.Events) > 0 && s.Events[0].Tick <= s.Tick {
		ev := heap.Pop(&s.Events).(Event)
		fin := ev.Kind == EvEnd
		s.apply(ev)
		if fin {
			return false
		}
	}
	if len(s.Events) == 0 && s.Link.Current < 0 && s.Sched.Empty() {
		return false
	}
	if !s.Link.Paused {
		budget := s.Link.Rate
		for budget > 0 {
			if s.Link.Current < 0 {
				p := s.Sched.Dequeue()
				if p == nil {
					break
				}
				s.Link.Current = p.ID
				s.Link.Remain = p.Len
			}
			p := s.Sched.Packets[s.Link.Current]
			n := budget
			if s.Link.Remain < n {
				n = s.Link.Remain
			}
			s.Link.Remain -= n
			budget -= n
			s.Served[p.FlowID] += int64(n)
			if s.Link.Remain == 0 {
				s.Trace = append(s.Trace, TraceEntry{
					Tick:     s.Tick,
					FlowID:   p.FlowID,
					PacketID: p.ID,
					Bytes:    p.Len,
				})
				s.Link.Current = -1
			}
		}
	}
	s.Tick++
	return true
}

// Run executes until the simulation is finished.
func (s *Sim) Run() {
	for s.Step() {
	}
}

// RunUntil executes up to (but not including) the given tick, or until the
// simulation finishes.
func (s *Sim) RunUntil(tick int) {
	for s.Tick < tick {
		if !s.Step() {
			return
		}
	}
}

// Save checkpoints the entire simulation state (event queue included) to JSON.
func (s *Sim) Save() ([]byte, error) {
	return json.Marshal(s)
}

// Load restores a simulation from a checkpoint. The restored simulation is
// bit-identical to the saved one and continues deterministically.
func Load(data []byte) (*Sim, error) {
	var s Sim
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	heap.Init(&s.Events)
	heap.Init(s.Sched)
	return &s, nil
}
