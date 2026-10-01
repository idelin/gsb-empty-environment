package sim

import (
	"sort"
)

// Scheduler is a two-level hierarchical WFQ scheduler.
//
// Virtual-time rules:
//   - The global virtual time V advances only while at least one tenant is
//     backlogged, at rate 1/sum(active tenant weights) per tick. While the
//     system is idle V is frozen, so no flow can gain or lose advantage by
//     idling.
//   - Each tenant has its own virtual time V_t advancing at
//     1/sum(active flow weights in t) per tick while the tenant is backlogged.
//   - A packet's flow-level virtual start is max(V_t, flow.VFinish) when the
//     flow was idle (reactivation cannot bank unlimited credit), and
//     flow.VFinish when continuously backlogged. Finish = start + bytes/weight.
//   - A tenant's global-domain start/finish for its current candidate packet
//     follows the same rule against V.
//   - On a weight change, queued finishes are rescaled around the current
//     virtual time: F' = V + (F - V) * (oldWeight/newWeight), preserving the
//     already-earned position while applying the new rate to remaining work.
//   - On flow withdrawal, queued packets are dropped, the flow leaves the
//     active set immediately, and virtual times stay continuous (no jump).
//
// Selection: among tenants pick the smallest (tenant.VFinish, candidate
// packet ID); within a tenant the candidate is the head packet with the
// smallest (VFinish, packet ID). Equal finish times are always broken by
// packet ID.
type Scheduler struct {
	Rate int // bytes per tick

	Tenants  map[int]*Tenant
	Flows    map[int]*Flow
	VTenant  map[int]float64
	VGlobal  float64
	LastTick int

	Index map[int]*Packet // queued packets by ID, for cancellation
}

func NewScheduler(rate int) *Scheduler {
	return &Scheduler{
		Rate:    rate,
		Tenants: map[int]*Tenant{},
		Flows:   map[int]*Flow{},
		VTenant: map[int]float64{},
		Index:   map[int]*Packet{},
	}
}

func (s *Scheduler) AddTenant(id int, weight float64) {
	s.Tenants[id] = &Tenant{ID: id, Weight: weight}
	s.VTenant[id] = 0
}

func (s *Scheduler) AddFlow(id, tenantID int, weight float64, capacity int) {
	s.Flows[id] = &Flow{ID: id, TenantID: tenantID, Weight: weight, Capacity: capacity}
}

func (s *Scheduler) sortedTenantIDs() []int {
	ids := make([]int, 0, len(s.Tenants))
	for id := range s.Tenants {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (s *Scheduler) sortedFlowIDs(tenantID int) []int {
	ids := make([]int, 0, len(s.Flows))
	for id, f := range s.Flows {
		if f.TenantID == tenantID {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// advanceTo moves all virtual times forward to the given tick. It is a
// no-op when the system is idle (virtual time freezes while there is no
// backlog). Iteration is in sorted order so floating-point accumulation is
// deterministic across runs and across snapshot/restore.
func (s *Scheduler) advanceTo(tick int) {
	dt := tick - s.LastTick
	if dt <= 0 {
		return
	}
	var wsum float64
	for _, id := range s.sortedTenantIDs() {
		if s.Tenants[id].Active {
			wsum += s.Tenants[id].Weight
		}
	}
	if wsum > 0 {
		s.VGlobal += float64(dt) / wsum
	}
	for _, tid := range s.sortedTenantIDs() {
		var fw float64
		for _, fid := range s.sortedFlowIDs(tid) {
			if s.Flows[fid].Active {
				fw += s.Flows[fid].Weight
			}
		}
		if fw > 0 {
			s.VTenant[tid] += float64(dt) / fw
		}
	}
	s.LastTick = tick
}

// Enqueue adds a packet to its flow, assigning flow-level virtual times and
// activating the flow (and its tenant) if needed.
func (s *Scheduler) Enqueue(p *Packet, tick int) {
	s.advanceTo(tick)
	f := s.Flows[p.FlowID]
	tn := s.Tenants[f.TenantID]
	if !f.Active {
		f.Active = true
		p.VStart = maxf(s.VTenant[tn.ID], f.VFinish)
	} else {
		p.VStart = f.VFinish
	}
	p.VFinish = p.VStart + float64(p.Bytes)/f.Weight
	f.VFinish = p.VFinish
	f.Queue = append(f.Queue, p)
	s.Index[p.ID] = p
	if !tn.Active {
		tn.Active = true
		tn.VStart = maxf(s.VGlobal, tn.VFinish)
		tn.VFinish = tn.VStart + float64(p.Bytes)/tn.Weight
	}
}

// candidate returns the tenant's head-of-queue packet with the smallest
// (VFinish, packet ID), or nil if the tenant has no backlog.
func (s *Scheduler) candidate(tn *Tenant) *Packet {
	var best *Packet
	for _, fid := range s.sortedFlowIDs(tn.ID) {
		f := s.Flows[fid]
		if !f.Active || len(f.Queue) == 0 {
			continue
		}
		head := f.Queue[0]
		if best == nil || lessPair(head.VFinish, head.ID, best.VFinish, best.ID) {
			best = head
		}
	}
	return best
}

func lessPair(f1 float64, id1 int, f2 float64, id2 int) bool {
	return f1 < f2 || (f1 == f2 && id1 < id2)
}

// Select picks the next packet to transmit and dequeues it. Returns nil when
// there is no backlog.
func (s *Scheduler) Select(tick int) *Packet {
	s.advanceTo(tick)
	var bestTn *Tenant
	var best *Packet
	for _, tid := range s.sortedTenantIDs() {
		tn := s.Tenants[tid]
		if !tn.Active {
			continue
		}
		cand := s.candidate(tn)
		if cand == nil {
			tn.Active = false
			continue
		}
		if best == nil || lessPair(tn.VFinish, cand.ID, bestTn.VFinish, best.ID) {
			bestTn, best = tn, cand
		}
	}
	if best == nil {
		return nil
	}
	f := s.Flows[best.FlowID]
	f.Queue = f.Queue[1:]
	delete(s.Index, best.ID)
	if len(f.Queue) == 0 {
		f.Active = false
	}
	// Chain the tenant's next candidate in the global virtual-time domain.
	next := s.candidate(bestTn)
	if next == nil {
		bestTn.Active = false
	} else {
		bestTn.VStart = bestTn.VFinish
		bestTn.VFinish = bestTn.VStart + float64(next.Bytes)/bestTn.Weight
	}
	return best
}

// SetFlowWeight changes a flow's weight, rescaling the virtual finishes of
// its queued packets around the tenant's current virtual time.
func (s *Scheduler) SetFlowWeight(flowID int, newWeight float64, tick int) {
	s.advanceTo(tick)
	f := s.Flows[flowID]
	old := f.Weight
	if old <= 0 || newWeight <= 0 {
		return
	}
	ratio := old / newWeight
	vt := s.VTenant[f.TenantID]
	for _, p := range f.Queue {
		p.VFinish = vt + (p.VFinish-vt)*ratio
	}
	f.VFinish = vt + (f.VFinish-vt)*ratio
	f.Weight = newWeight
}

// SetTenantWeight changes a tenant's weight, rescaling its global-domain
// finish around the current global virtual time.
func (s *Scheduler) SetTenantWeight(tenantID int, newWeight float64, tick int) {
	s.advanceTo(tick)
	tn := s.Tenants[tenantID]
	old := tn.Weight
	if old <= 0 || newWeight <= 0 {
		return
	}
	ratio := old / newWeight
	tn.VFinish = s.VGlobal + (tn.VFinish-s.VGlobal)*ratio
	tn.Weight = newWeight
}

// CancelPacket removes a queued packet by ID. Returns true exactly once per
// packet: cancelling a packet that is not queued (already served, already
// cancelled, in transmission, or unknown) is a no-op returning false, so a
// cancelled packet can never be counted twice.
func (s *Scheduler) CancelPacket(packetID int, tick int) bool {
	s.advanceTo(tick)
	p, ok := s.Index[packetID]
	if !ok {
		return false
	}
	f := s.Flows[p.FlowID]
	for i, q := range f.Queue {
		if q.ID == packetID {
			f.Queue = append(f.Queue[:i], f.Queue[i+1:]...)
			break
		}
	}
	delete(s.Index, packetID)
	p.Cancelled = true
	if len(f.Queue) == 0 {
		f.Active = false
	}
	s.refreshTenant(f.TenantID)
	return true
}

// WithdrawFlow removes a flow from the scheduler, dropping its queued
// packets. Returns the number of dropped packets. Virtual times stay
// continuous; the tenant leaves the active set if it has no backlog left.
func (s *Scheduler) WithdrawFlow(flowID int, tick int) int {
	s.advanceTo(tick)
	f := s.Flows[flowID]
	f.Withdrawn = true
	n := len(f.Queue)
	for _, p := range f.Queue {
		p.Dropped = true
		delete(s.Index, p.ID)
	}
	f.Queue = nil
	f.Active = false
	s.refreshTenant(f.TenantID)
	return n
}

func (s *Scheduler) refreshTenant(tenantID int) {
	tn := s.Tenants[tenantID]
	for _, fid := range s.sortedFlowIDs(tenantID) {
		if s.Flows[fid].Active {
			return
		}
	}
	tn.Active = false
}

// QueuedPackets returns the total number of packets waiting in all flows.
func (s *Scheduler) QueuedPackets() int {
	n := 0
	for _, f := range s.Flows {
		n += len(f.Queue)
	}
	return n
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
