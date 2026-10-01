package sim

import (
	"math"
	"sort"
)

// FluidFlow describes one flow for the ideal fluid (GPS) reference
// scheduler. Arrivals are byte amounts injected at a tick; weight changes
// apply from their tick on.
type FluidFlow struct {
	FlowID       int
	TenantID     int
	Weight       float64
	TenantWeight float64
	Arrivals     []FluidArrival
	WeightEvents []FluidWeightEvent
}

type FluidArrival struct {
	Tick  int
	Bytes int
}

type FluidWeightEvent struct {
	Tick   int
	Weight float64
}

// FluidServe simulates an ideal hierarchical fluid scheduler at the given
// byte-per-tick rate and returns, per flow ID, the cumulative bytes served
// at each tick (slice of length until+1). It is an independent reference
// implementation sharing no code with the packet scheduler.
func FluidServe(flows []FluidFlow, rate float64, until int) map[int][]float64 {
	tenants := map[int]*fluidTenant{}
	fluidFlows := map[int]*fluidFlow{}
	flowIDs := []int{}
	for _, ff := range flows {
		tn, ok := tenants[ff.TenantID]
		if !ok {
			tn = &fluidTenant{id: ff.TenantID, weight: ff.TenantWeight, flows: map[int]*fluidFlow{}}
			tenants[ff.TenantID] = tn
		}
		f := &fluidFlow{id: ff.FlowID, weight: ff.Weight}
		tn.flows[f.id] = f
		fluidFlows[f.id] = f
		flowIDs = append(flowIDs, f.id)
	}
	sort.Ints(flowIDs)

	svc := map[int][]float64{}
	for _, id := range flowIDs {
		svc[id] = make([]float64, until+1)
	}

	arrivalsByTick := map[int][]FluidArrival{}
	flowOfArrival := map[int][]int{}
	for _, ff := range flows {
		for _, a := range ff.Arrivals {
			arrivalsByTick[a.Tick] = append(arrivalsByTick[a.Tick], a)
			flowOfArrival[a.Tick] = append(flowOfArrival[a.Tick], ff.FlowID)
		}
	}
	weightEvents := map[int][]FluidWeightEvent{}
	weightFlow := map[int][]int{}
	for _, ff := range flows {
		for _, we := range ff.WeightEvents {
			weightEvents[we.Tick] = append(weightEvents[we.Tick], we)
			weightFlow[we.Tick] = append(weightFlow[we.Tick], ff.FlowID)
		}
	}

	for t := 0; t < until; t++ {
		for i, a := range arrivalsByTick[t] {
			fluidFlows[flowOfArrival[t][i]].backlog += float64(a.Bytes)
		}
		for i, we := range weightEvents[t] {
			fluidFlows[weightFlow[t][i]].weight = we.Weight
		}
		distribute(rate, tenants)
		for _, id := range flowIDs {
			svc[id][t+1] = fluidFlows[id].served
		}
	}
	return svc
}

type fluidFlow struct {
	id      int
	weight  float64
	backlog float64
	served  float64
}

type fluidTenant struct {
	id     int
	weight float64
	flows  map[int]*fluidFlow
}

const eps = 1e-12

func (t *fluidTenant) backlog() float64 {
	var b float64
	for _, f := range t.flows {
		b += f.backlog
	}
	return b
}

func sortedTenantList(tenants map[int]*fluidTenant) []*fluidTenant {
	ids := make([]int, 0, len(tenants))
	for id := range tenants {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]*fluidTenant, 0, len(ids))
	for _, id := range ids {
		out = append(out, tenants[id])
	}
	return out
}

func activeFlows(tn *fluidTenant) []*fluidFlow {
	ids := make([]int, 0, len(tn.flows))
	for id := range tn.flows {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []*fluidFlow{}
	for _, id := range ids {
		if tn.flows[id].backlog > eps {
			out = append(out, tn.flows[id])
		}
	}
	return out
}

// distribute splits capacity among backlogged tenants proportionally to
// tenant weights, and within each tenant proportionally to flow weights,
// redistributing unused shares (water-filling) until capacity is exhausted
// or no backlog remains.
func distribute(capacity float64, tenants map[int]*fluidTenant) {
	remaining := capacity
	active := []*fluidTenant{}
	for _, tn := range sortedTenantList(tenants) {
		if tn.backlog() > eps {
			active = append(active, tn)
		}
	}
	for remaining > eps && len(active) > 0 {
		var wsum float64
		for _, tn := range active {
			wsum += tn.weight
		}
		used := 0.0
		next := []*fluidTenant{}
		for _, tn := range active {
			share := remaining * tn.weight / wsum
			got := serveFlows(share, activeFlows(tn))
			used += got
			if tn.backlog() > eps {
				next = append(next, tn)
			}
		}
		remaining -= used
		if used < eps {
			break
		}
		active = next
	}
}

func serveFlows(amount float64, flows []*fluidFlow) (used float64) {
	remaining := amount
	active := flows
	for remaining > eps && len(active) > 0 {
		var wsum float64
		for _, f := range active {
			wsum += f.weight
		}
		round := 0.0
		next := []*fluidFlow{}
		for _, f := range active {
			share := remaining * f.weight / wsum
			got := math.Min(share, f.backlog)
			f.backlog -= got
			f.served += got
			round += got
			if f.backlog > eps {
				next = append(next, f)
			}
		}
		remaining -= round
		used += round
		if round < eps {
			break
		}
		active = next
	}
	return used
}

// DeviationReport summarizes the byte-level service gap between the packet
// scheduler and the ideal fluid reference.
type DeviationReport struct {
	Until      int
	MaxBytes   float64
	WorstFlow  int
	WorstTick  int
	PerFlowMax map[int]float64
}

// ByteDeviation compares cumulative bytes served per flow per tick between
// the packet trace (bytes credited at transmission completion) and the
// fluid reference, returning the maximum absolute deviation in bytes.
func ByteDeviation(trace []TraceRecord, fluid map[int][]float64, until int) DeviationReport {
	pkt := map[int][]float64{}
	for _, rec := range trace {
		arr, ok := pkt[rec.FlowID]
		if !ok {
			arr = make([]float64, until+1)
			pkt[rec.FlowID] = arr
		}
		if rec.End <= until {
			arr[rec.End] += float64(rec.Bytes)
		}
	}
	flowIDs := map[int]bool{}
	for id := range fluid {
		flowIDs[id] = true
	}
	for id := range pkt {
		flowIDs[id] = true
	}
	rep := DeviationReport{Until: until, PerFlowMax: map[int]float64{}}
	for id := range flowIDs {
		p := pkt[id]
		if p == nil {
			p = make([]float64, until+1)
		}
		fl := fluid[id]
		cum := 0.0
		for t := 0; t <= until; t++ {
			cum += p[t]
			var f float64
			if t < len(fl) {
				f = fl[t]
			}
			d := math.Abs(cum - f)
			if d > rep.PerFlowMax[id] {
				rep.PerFlowMax[id] = d
			}
			if d > rep.MaxBytes {
				rep.MaxBytes = d
				rep.WorstFlow = id
				rep.WorstTick = t
			}
		}
	}
	return rep
}
