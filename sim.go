package main

import "sort"

// packet is an in-flight unit of traffic. All movement happens on integer
// ticks; no real network traffic is involved.
type packet struct {
	flow    int
	nextHop int // index into the flow's resolved route
}

// simLink is one link in the simulation: per-direction FIFO queues plus a
// service accumulator. ModeSeparate gives each direction its own capacity
// tokens per tick; ModeShared draws both directions from one token budget.
type simLink struct {
	capacity  float64
	mode      string
	queue     [2][]packet
	incoming  [2][]packet // arrivals applied at the start of the next tick
	svcAcc    [2]float64  // separate-mode token accumulators
	sharedAcc float64     // shared-mode token accumulator
	rr        int         // round-robin tie-break for shared mode

	maxQueue [2]int
	served   [2]int
}

// FlowSimStat records per-flow simulation outcomes.
type FlowSimStat struct {
	FlowID      string
	Unreachable bool
	Generated   int
	Delivered   int
	Backlog     int // generated - delivered at end of run
	OK          bool
}

// LinkSimStat records per-link, per-direction simulation outcomes.
type LinkSimStat struct {
	LinkID    string
	Dir       int
	MaxQueue  int
	EndQueue  int
	MidQueue  int
	Served    int
	Congested bool // queue still growing in the second half of the run
}

// SimResult is the outcome of one simulated scenario.
type SimResult struct {
	ScenarioID  string
	Ticks       int
	Flows       []FlowSimStat
	Links       []LinkSimStat
	Unreachable []string
}

// Simulate runs the discrete-event model for the given failure set (empty =
// baseline) for ticks integer ticks. Flows whose fixed route crosses a failed
// link are excluded and reported as unreachable — never as zero-load success.
func Simulate(n *Network, scenarioID string, failed map[int]bool, ticks int) SimResult {
	res := SimResult{ScenarioID: scenarioID, Ticks: ticks}

	unreach := map[int]bool{}
	for _, fi := range n.UnreachableFlows(failed) {
		unreach[fi] = true
		res.Unreachable = append(res.Unreachable, n.Flows[fi].ID)
	}
	sort.Strings(res.Unreachable)

	links := make([]*simLink, len(n.Links))
	for i, l := range n.Links {
		links[i] = &simLink{capacity: l.Capacity, mode: l.Mode}
	}
	active := activeFlows(n, unreach)
	genAcc := make([]float64, len(n.Flows))
	generated := make([]int, len(n.Flows))
	delivered := make([]int, len(n.Flows))
	midQueue := make([][2]int, len(n.Links))
	mid := ticks / 2

	for t := 0; t < ticks; t++ {
		// 1. Arrivals from the previous tick become visible.
		for _, sl := range links {
			for d := 0; d < 2; d++ {
				if len(sl.incoming[d]) > 0 {
					sl.queue[d] = append(sl.queue[d], sl.incoming[d]...)
					sl.incoming[d] = sl.incoming[d][:0]
				}
			}
		}
		// 2. Sources inject packets according to demand (fractional rates are
		//    emitted by an integer accumulator, preserving long-run rate).
		for _, fi := range active {
			genAcc[fi] += n.Flows[fi].Demand
			k := int(genAcc[fi])
			genAcc[fi] -= float64(k)
			generated[fi] += k
			first := n.Routes[fi][0]
			for j := 0; j < k; j++ {
				sl := links[first.LinkIdx]
				sl.incoming[first.Dir] = append(sl.incoming[first.Dir], packet{flow: fi, nextHop: 0})
			}
		}
		// 3. Each link serves up to its capacity from its queues.
		for _, sl := range links {
			if sl.mode == ModeShared {
				sl.sharedAcc += sl.capacity
				tokens := int(sl.sharedAcc)
				sl.sharedAcc -= float64(tokens)
				for tokens > 0 && (len(sl.queue[0]) > 0 || len(sl.queue[1]) > 0) {
					d := sl.rr
					if len(sl.queue[d]) == 0 {
						d = 1 - d
					}
					if len(sl.queue[d]) == 0 {
						break
					}
					p := sl.queue[d][0]
					sl.queue[d] = sl.queue[d][1:]
					sl.served[d]++
					tokens--
					sl.rr = 1 - d
					forward(n, links, p, &delivered)
				}
			} else {
				for d := 0; d < 2; d++ {
					sl.svcAcc[d] += sl.capacity
					tokens := int(sl.svcAcc[d])
					sl.svcAcc[d] -= float64(tokens)
					for tokens > 0 && len(sl.queue[d]) > 0 {
						p := sl.queue[d][0]
						sl.queue[d] = sl.queue[d][1:]
						sl.served[d]++
						tokens--
						forward(n, links, p, &delivered)
					}
				}
			}
			for d := 0; d < 2; d++ {
				if len(sl.queue[d]) > sl.maxQueue[d] {
					sl.maxQueue[d] = len(sl.queue[d])
				}
			}
		}
		if t == mid {
			for li, sl := range links {
				for d := 0; d < 2; d++ {
					midQueue[li][d] = len(sl.queue[d])
				}
			}
		}
	}

	for fi := range n.Flows {
		st := FlowSimStat{FlowID: n.Flows[fi].ID, Unreachable: unreach[fi]}
		if !st.Unreachable {
			st.Generated = generated[fi]
			st.Delivered = delivered[fi]
			st.Backlog = generated[fi] - delivered[fi]
			// OK means the backlog is just pipeline fill: bounded by one
			// tick of demand per route hop. A persistently growing queue
			// (overload) quickly exceeds this bound.
			bound := n.Flows[fi].Demand*float64(len(n.Routes[fi])+1) + 1
			st.OK = float64(st.Backlog) <= bound
		}
		res.Flows = append(res.Flows, st)
	}
	for li, sl := range links {
		for d := 0; d < 2; d++ {
			end := len(sl.queue[d])
			ls := LinkSimStat{
				LinkID:   n.Links[li].ID,
				Dir:      d,
				MaxQueue: sl.maxQueue[d],
				EndQueue: end,
				MidQueue: midQueue[li][d],
				Served:   sl.served[d],
			}
			// Congested: backlog kept growing through the second half and is
			// not just pipeline fill (more than one tick of capacity).
			ls.Congested = end > midQueue[li][d] && float64(end) > sl.capacity
			if ls.MaxQueue > 0 || ls.Served > 0 {
				res.Links = append(res.Links, ls)
			}
		}
	}
	return res
}

// forward pushes a packet to its next hop, or counts it as delivered when it
// leaves the last link of its route.
func forward(n *Network, links []*simLink, p packet, delivered *[]int) {
	route := n.Routes[p.flow]
	if p.nextHop+1 >= len(route) {
		(*delivered)[p.flow]++
		return
	}
	next := route[p.nextHop+1]
	sl := links[next.LinkIdx]
	sl.incoming[next.Dir] = append(sl.incoming[next.Dir], packet{flow: p.flow, nextHop: p.nextHop + 1})
}
