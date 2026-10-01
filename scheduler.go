// Package wfqsim implements a discrete-event, integer-tick simulation of a
// weighted-fair packet scheduler with virtual finish times (SFQ-style tags),
// hierarchical tenants, dynamic weights, and a byte-level comparison against
// an ideal fluid (GPS) scheduler. No real network traffic is generated.
package wfqsim

import "container/heap"

// Packet is a unit of work. S and F are the virtual start/finish tags.
type Packet struct {
	ID       int     `json:"id"`
	FlowID   int     `json:"flow"`
	TenantID int     `json:"tenant"`
	Len      int     `json:"len"` // bytes
	Arrive   int     `json:"arrive"`
	S        float64 `json:"s"`
	F        float64 `json:"f"`
	Dropped  bool    `json:"dropped,omitempty"` // cancelled or withdrawn; never counted
}

// Flow is a scheduling leaf inside a tenant.
type Flow struct {
	ID       int     `json:"id"`
	TenantID int     `json:"tenant"`
	Weight   float64 `json:"weight"`
	LastF    float64 `json:"lastF"`  // finish tag of the last enqueued packet
	Queue    []int   `json:"queue"`  // queued packet IDs, FIFO
	Active   bool    `json:"active"` // false after withdrawal
}

// Tenant groups flows; the link is shared among tenants by tenant weight.
type Tenant struct {
	ID     int     `json:"id"`
	Weight float64 `json:"weight"`
}

// Scheduler assigns virtual tags and picks the next packet to serve.
//
// Virtual-time rules (see README.md):
//   - V is the start tag of the packet currently in service (SFQ rule);
//     it freezes while the link is idle or paused.
//   - Enqueue: S = max(V, flow.LastF), F = S + Len / effWeight.
//   - effWeight = tenant.Weight * flow.Weight (flattened hierarchy).
//   - An idle flow reactivates with S based on the *current* V, so it
//     cannot accumulate unbounded advantage while idle.
//   - A weight change from wOld to wNew rescales all of the flow's queued
//     tags around V: tag' = V + (tag - V) * wOld / wNew.
//   - Withdrawal drops all queued packets of the flow (never counted);
//     a packet already in service finishes; V is unchanged.
//   - Ties on F are broken by smaller packet ID.
type Scheduler struct {
	V       float64           `json:"v"`
	Tenants map[int]*Tenant   `json:"tenants"`
	Flows   map[int]*Flow     `json:"flows"`
	Packets map[int]*Packet   `json:"packets"`
	PQ      []int             `json:"pq"` // heap of packet IDs ordered by (F, ID)
}

func NewScheduler(tenants []Tenant, flows []Flow) *Scheduler {
	s := &Scheduler{
		Tenants: make(map[int]*Tenant),
		Flows:   make(map[int]*Flow),
		Packets: make(map[int]*Packet),
	}
	for i := range tenants {
		t := tenants[i]
		s.Tenants[t.ID] = &t
	}
	for i := range flows {
		f := flows[i]
		f.Active = true
		s.Flows[f.ID] = &f
	}
	return s
}

func (s *Scheduler) effWeight(f *Flow) float64 {
	return s.Tenants[f.TenantID].Weight * f.Weight
}

// heap.Interface over PQ, ordered by (F, ID).

func (s *Scheduler) Len() int { return len(s.PQ) }

func (s *Scheduler) Less(i, j int) bool {
	a, b := s.Packets[s.PQ[i]], s.Packets[s.PQ[j]]
	if a.F != b.F {
		return a.F < b.F
	}
	return a.ID < b.ID // tie on finish time: smaller packet ID first
}

func (s *Scheduler) Swap(i, j int) { s.PQ[i], s.PQ[j] = s.PQ[j], s.PQ[i] }

func (s *Scheduler) Push(x interface{}) { s.PQ = append(s.PQ, x.(int)) }

func (s *Scheduler) Pop() interface{} {
	old := s.PQ
	n := len(old)
	v := old[n-1]
	s.PQ = old[:n-1]
	return v
}

// Enqueue tags a packet and pushes it into the scheduler heap.
func (s *Scheduler) Enqueue(p *Packet) {
	f := s.Flows[p.FlowID]
	start := s.V
	if f.LastF > start {
		start = f.LastF
	}
	p.S = start
	p.F = start + float64(p.Len)/s.effWeight(f)
	f.LastF = p.F
	f.Queue = append(f.Queue, p.ID)
	s.Packets[p.ID] = p
	heap.Push(s, p.ID)
}

// Dequeue pops the eligible packet with the smallest (F, ID) and advances V
// to its start tag. Dropped packets and packets of withdrawn flows are
// discarded lazily and never returned.
func (s *Scheduler) Dequeue() *Packet {
	for len(s.PQ) > 0 {
		id := s.PQ[0]
		p := s.Packets[id]
		f := s.Flows[p.FlowID]
		if p.Dropped || !f.Active {
			p.Dropped = true
			heap.Pop(s)
			continue
		}
		heap.Pop(s)
		f.Queue = f.Queue[1:]
		if p.S > s.V {
			s.V = p.S
		}
		return p
	}
	return nil
}

// Empty reports whether no servable packet remains.
func (s *Scheduler) Empty() bool {
	for _, id := range s.PQ {
		p := s.Packets[id]
		if !p.Dropped && s.Flows[p.FlowID].Active {
			return false
		}
	}
	return true
}

// rescale re-tags a flow's queued packets after its effective weight changed
// from wOld to wNew: tag' = V + (tag - V) * wOld / wNew.
func (s *Scheduler) rescale(f *Flow, ratio float64) {
	for _, id := range f.Queue {
		p := s.Packets[id]
		if p.Dropped {
			continue
		}
		p.S = s.V + (p.S-s.V)*ratio
		p.F = s.V + (p.F-s.V)*ratio
	}
	f.LastF = s.V + (f.LastF-s.V)*ratio
	heap.Init(s) // tags changed arbitrarily; restore heap order
}

// SetFlowWeight changes a flow's weight and rescales its queued tags.
func (s *Scheduler) SetFlowWeight(flowID int, w float64) {
	f := s.Flows[flowID]
	old := s.effWeight(f)
	f.Weight = w
	s.rescale(f, old/s.effWeight(f))
}

// SetTenantWeight changes a tenant's weight and rescales every flow in it.
func (s *Scheduler) SetTenantWeight(tenantID int, w float64) {
	t := s.Tenants[tenantID]
	old := t.Weight
	t.Weight = w
	for _, f := range s.Flows {
		if f.TenantID != tenantID || !f.Active {
			continue
		}
		s.rescale(f, (old*f.Weight)/(t.Weight*f.Weight))
	}
}

// Withdraw removes a flow: queued packets are dropped (never served, never
// counted); a packet already in service is allowed to finish. V is unchanged
// and the freed share is redistributed automatically by the tag order.
func (s *Scheduler) Withdraw(flowID int) {
	f := s.Flows[flowID]
	if !f.Active {
		return
	}
	f.Active = false
	for _, id := range f.Queue {
		s.Packets[id].Dropped = true
	}
	f.Queue = nil
}

// Cancel drops a queued packet. It returns false if the packet is unknown,
// already dropped, or currently in service (in-service packets cannot be
// cancelled), so a cancelled packet can never be counted twice.
func (s *Scheduler) Cancel(packetID int) bool {
	p, ok := s.Packets[packetID]
	if !ok || p.Dropped {
		return false
	}
	f := s.Flows[p.FlowID]
	for i, id := range f.Queue {
		if id == packetID {
			f.Queue = append(f.Queue[:i], f.Queue[i+1:]...)
			p.Dropped = true
			return true
		}
	}
	return false // not in the queue: already in service or already served
}
