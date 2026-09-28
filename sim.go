package flowlb

import "container/heap"

// Packet is one simulated packet. All times are integer ticks.
type Packet struct {
	FlowID  string
	Seq     int
	Size    int   // bytes
	Arrival int64 // tick at which the packet enters a path queue
}

// arrival is a scheduled packet-generation event.
type arrival struct {
	tick int64
	pkt  Packet
}

// arrivalHeap is a min-heap ordered by tick, then flow ID, then sequence,
// so simulation results are fully deterministic.
type arrivalHeap []arrival

func (h arrivalHeap) Len() int { return len(h) }
func (h arrivalHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.tick != b.tick {
		return a.tick < b.tick
	}
	if a.pkt.FlowID != b.pkt.FlowID {
		return a.pkt.FlowID < b.pkt.FlowID
	}
	return a.pkt.Seq < b.pkt.Seq
}
func (h arrivalHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *arrivalHeap) Push(x interface{}) { *h = append(*h, x.(arrival)) }
func (h *arrivalHeap) Pop() interface{} {
	old := *h
	n := len(old)
	a := old[n-1]
	*h = old[:n-1]
	return a
}

// PathStats accumulates per-path simulation results.
type PathStats struct {
	Delivered int
	Bytes     int
	MaxQueue  int
	TotalWait int64 // sum of (departure tick - arrival tick)
}

// AvgWait returns the mean queueing delay in ticks.
func (s PathStats) AvgWait() float64 {
	if s.Delivered == 0 {
		return 0
	}
	return float64(s.TotalWait) / float64(s.Delivered)
}

// Sim is a tick-stepped discrete-event simulator: packet arrivals are events
// on an integer-tick timeline, each path owns a FIFO queue, and each link
// serves up to Rate packets per tick. No real network traffic is involved.
type Sim struct {
	a         *Assigner
	arrivals  arrivalHeap
	queues    map[string][]Packet
	stats     map[string]*PathStats
	Generated int // packets injected into the simulation
	Dropped   int // arrivals with no eligible path
}

func NewSim(a *Assigner) *Sim {
	return &Sim{
		a:      a,
		queues: make(map[string][]Packet),
		stats:  make(map[string]*PathStats),
	}
}

// Stats returns the accumulated stats for pathID (never nil).
func (s *Sim) Stats(pathID string) *PathStats {
	st, ok := s.stats[pathID]
	if !ok {
		st = &PathStats{}
		s.stats[pathID] = st
	}
	return st
}

// AddFlow schedules deterministic packet arrivals for flowID in
// [start, until). Interval, phase and packet size are derived from the flow
// ID so runs are reproducible without a random source.
func (s *Sim) AddFlow(flowID string, start, until int64) {
	interval := 1 + int64(unitInterval(flowID, "interval")*4) // 1..4 ticks
	offset := int64(unitInterval(flowID, "offset") * float64(interval))
	size := 64 + int(unitInterval(flowID, "size")*1336) // 64..1399 bytes
	seq := 0
	for t := start + offset; t < until; t += interval {
		heap.Push(&s.arrivals, arrival{tick: t, pkt: Packet{
			FlowID:  flowID,
			Seq:     seq,
			Size:    size,
			Arrival: t,
		}})
		seq++
	}
}

// Run advances the simulation to tick `until`, processing arrival events in
// tick order and draining each path queue at its link rate every tick.
func (s *Sim) Run(until int64) {
	for t := int64(0); t < until; t++ {
		for s.arrivals.Len() > 0 && s.arrivals[0].tick <= t {
			ar := heap.Pop(&s.arrivals).(arrival)
			s.Generated++
			pathID, ok := s.a.Assign(ar.pkt.FlowID)
			if !ok {
				s.Dropped++
				continue
			}
			s.queues[pathID] = append(s.queues[pathID], ar.pkt)
		}
		for _, p := range s.a.Paths() {
			q := s.queues[p.ID]
			n := p.Rate
			if n > len(q) {
				n = len(q)
			}
			st := s.Stats(p.ID)
			for i := 0; i < n; i++ {
				st.Delivered++
				st.Bytes += q[i].Size
				st.TotalWait += t - q[i].Arrival
			}
			q = q[n:]
			if len(q) > st.MaxQueue {
				st.MaxQueue = len(q)
			}
			s.queues[p.ID] = q
		}
	}
}

// Backlog returns the number of packets still queued on pathID.
func (s *Sim) Backlog(pathID string) int { return len(s.queues[pathID]) }
