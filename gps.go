package wfqsim

import (
	"container/heap"
	"sort"
)

// GPSSim is an independent ideal fluid (Generalized Processor Sharing)
// reference. It replays the same event log as the packet simulator but
// serves bytes fractionally: at every tick the link rate is split among
// backlogged flows in exact proportion to their effective weights
// (tenant weight * flow weight). It is used only as a yardstick to measure
// the packet scheduler's per-flow byte deviation.
type GPSSim struct {
	Tick     int              `json:"tick"`
	Seq      int              `json:"seq"`
	Rate     int              `json:"rate"`
	Paused   bool             `json:"paused"`
	Events   EventQueue       `json:"events"`
	FlowW    map[int]float64  `json:"flowW"`
	TenantOf map[int]int      `json:"tenantOf"`
	TenantW  map[int]float64  `json:"tenantW"`
	Active   map[int]bool     `json:"active"`
	Backlog  map[int]float64  `json:"backlog"`  // unserved bytes per flow
	PktRem   map[int]float64  `json:"pktRem"`   // unserved bytes per packet
	PktFlow  map[int]int      `json:"pktFlow"`  // packet -> flow
	PktQueue map[int][]int    `json:"pktQueue"` // flow -> packet IDs, FIFO
	Served   map[int]float64  `json:"served"`   // bytes served per flow
	Marks    []GMark          `json:"marks"`
}

// GMark is a GPS checkpoint of per-flow served bytes.
type GMark struct {
	Tick  int             `json:"tick"`
	Bytes map[int]float64 `json:"bytes"`
}

func NewGPS(rate int, tenants []Tenant, flows []Flow) *GPSSim {
	g := &GPSSim{
		Rate:     rate,
		FlowW:    make(map[int]float64),
		TenantOf: make(map[int]int),
		TenantW:  make(map[int]float64),
		Active:   make(map[int]bool),
		Backlog:  make(map[int]float64),
		PktRem:   make(map[int]float64),
		PktFlow:  make(map[int]int),
		PktQueue: make(map[int][]int),
		Served:   make(map[int]float64),
	}
	for _, t := range tenants {
		g.TenantW[t.ID] = t.Weight
	}
	for _, f := range flows {
		g.FlowW[f.ID] = f.Weight
		g.TenantOf[f.ID] = f.TenantID
		g.Active[f.ID] = true
	}
	return g
}

// Add submits the same event to the GPS reference (sequence numbers are
// assigned independently but identically, given the same submission order).
func (g *GPSSim) Add(ev Event) {
	ev.Seq = g.Seq
	g.Seq++
	heap.Push(&g.Events, ev)
}

func (g *GPSSim) effW(flow int) float64 {
	return g.FlowW[flow] * g.TenantW[g.TenantOf[flow]]
}

func (g *GPSSim) apply(ev Event) {
	switch ev.Kind {
	case EvCancel:
		rem, ok := g.PktRem[ev.PacketID]
		if !ok {
			return
		}
		f := g.PktFlow[ev.PacketID]
		g.Backlog[f] -= rem
		delete(g.PktRem, ev.PacketID)
	case EvWithdraw:
		g.Active[ev.FlowID] = false
		g.Backlog[ev.FlowID] = 0
		for _, id := range g.PktQueue[ev.FlowID] {
			delete(g.PktRem, id)
		}
		g.PktQueue[ev.FlowID] = nil
	case EvWeightFlow:
		g.FlowW[ev.FlowID] = ev.Weight
	case EvWeightTenant:
		g.TenantW[ev.TenantID] = ev.Weight
	case EvPause:
		g.Paused = true
	case EvResume:
		g.Paused = false
	case EvArrive:
		p := ev.Packet
		g.Backlog[p.FlowID] += float64(p.Len)
		g.PktRem[p.ID] = float64(p.Len)
		g.PktFlow[p.ID] = p.FlowID
		g.PktQueue[p.FlowID] = append(g.PktQueue[p.FlowID], p.ID)
	case EvCheckpoint:
		m := GMark{Tick: g.Tick, Bytes: make(map[int]float64, len(g.Served))}
		for k, v := range g.Served {
			m.Bytes[k] = v
		}
		g.Marks = append(g.Marks, m)
	}
}

// serve distributes one tick of capacity among backlogged flows by exact
// weight-proportional water filling.
func (g *GPSSim) serve() {
	if g.Paused {
		return
	}
	remaining := float64(g.Rate)
	const eps = 1e-9
	for remaining > eps {
		totalW := 0.0
		for f, b := range g.Backlog {
			if g.Active[f] && b > eps {
				totalW += g.effW(f)
			}
		}
		if totalW == 0 {
			return
		}
		hungry := make([]int, 0, len(g.Backlog))
		for f, b := range g.Backlog {
			if g.Active[f] && b > eps {
				hungry = append(hungry, f)
			}
		}
		sort.Ints(hungry)
		used := 0.0
		progress := false
		for _, f := range hungry {
			b := g.Backlog[f]
			share := remaining * g.effW(f) / totalW
			if share > b {
				share = b
			}
			if share <= 0 {
				continue
			}
			g.Backlog[f] = b - share
			g.Served[f] += share
			g.drainPackets(f, share)
			used += share
			progress = true
		}
		remaining -= used
		if !progress {
			return
		}
	}
}

// drainPackets attributes served bytes to the flow's packets FIFO, so a
// later cancellation subtracts the correct unserved remainder.
func (g *GPSSim) drainPackets(flow int, bytes float64) {
	q := g.PktQueue[flow]
	for bytes > 0 && len(q) > 0 {
		id := q[0]
		rem := g.PktRem[id]
		if rem <= bytes {
			bytes -= rem
			delete(g.PktRem, id)
			q = q[1:]
		} else {
			g.PktRem[id] = rem - bytes
			bytes = 0
		}
	}
	g.PktQueue[flow] = q
}

// Step advances the GPS reference by one tick.
func (g *GPSSim) Step() bool {
	for len(g.Events) > 0 && g.Events[0].Tick <= g.Tick {
		ev := heap.Pop(&g.Events).(Event)
		fin := ev.Kind == EvEnd
		g.apply(ev)
		if fin {
			return false
		}
	}
	if len(g.Events) == 0 {
		idle := true
		for f, b := range g.Backlog {
			if g.Active[f] && b > 1e-9 {
				idle = false
				break
			}
		}
		if idle {
			return false
		}
	}
	g.serve()
	g.Tick++
	return true
}

// Run executes the GPS reference to completion.
func (g *GPSSim) Run() {
	for g.Step() {
	}
}

// FlowDeviation is the byte-level service difference between the packet
// scheduler and the GPS reference for one flow.
type FlowDeviation struct {
	FlowID int
	Sched  int64   // bytes served by the packet scheduler
	GPS    float64 // bytes served by the ideal fluid scheduler
	Diff   float64 // Sched - GPS
}

// CompareServed reports per-flow byte deviations at the end of both runs.
func CompareServed(s *Sim, g *GPSSim) []FlowDeviation {
	flows := make(map[int]bool)
	for f := range s.Served {
		flows[f] = true
	}
	for f := range g.Served {
		flows[f] = true
	}
	ids := make([]int, 0, len(flows))
	for f := range flows {
		ids = append(ids, f)
	}
	sort.Ints(ids)
	out := make([]FlowDeviation, 0, len(ids))
	for _, f := range ids {
		d := FlowDeviation{FlowID: f, Sched: s.Served[f], GPS: g.Served[f]}
		d.Diff = float64(d.Sched) - d.GPS
		out = append(out, d)
	}
	return out
}

// CompareMarks reports per-flow byte deviations at every checkpoint tick.
func CompareMarks(s *Sim, g *GPSSim) map[int][]FlowDeviation {
	out := make(map[int][]FlowDeviation)
	n := len(s.Marks)
	if len(g.Marks) < n {
		n = len(g.Marks)
	}
	for i := 0; i < n; i++ {
		sm, gm := s.Marks[i], g.Marks[i]
		flows := make(map[int]bool)
		for f := range sm.Bytes {
			flows[f] = true
		}
		for f := range gm.Bytes {
			flows[f] = true
		}
		ids := make([]int, 0, len(flows))
		for f := range flows {
			ids = append(ids, f)
		}
		sort.Ints(ids)
		for _, f := range ids {
			out[sm.Tick] = append(out[sm.Tick], FlowDeviation{
				FlowID: f,
				Sched:  sm.Bytes[f],
				GPS:    gm.Bytes[f],
				Diff:   float64(sm.Bytes[f]) - gm.Bytes[f],
			})
		}
	}
	return out
}
