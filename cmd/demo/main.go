// Command demo runs a small hierarchical-WFQ simulation and prints the
// service trace plus a byte-level deviation report against an independent
// ideal fluid (GPS) reference.
package main

import (
	"fmt"

	"wfqsim/sim"
)

func main() {
	const until = 240

	e := sim.NewEngine(1) // link rate: 1 byte/tick
	e.AddTenant(1, 2)     // "gold" tenant
	e.AddTenant(2, 1)     // "silver" tenant
	e.AddFlow(1, 1, 1, 0) // 10-byte packets
	e.AddFlow(2, 1, 1, 0) // 4-byte packets
	e.AddFlow(3, 2, 1, 0) // 6-byte packets

	arrive := func(tick, firstID, flow, n, size int) {
		for i := 0; i < n; i++ {
			e.Arrive(tick, firstID+i, flow, size)
		}
	}
	arrive(0, 1, 1, 8, 10)
	arrive(0, 21, 2, 8, 4)
	arrive(0, 41, 3, 6, 6)
	e.Submit(sim.Event{Tick: 10, Type: sim.EvCancel, PacketID: 43})              // cancel a queued packet
	e.Submit(sim.Event{Tick: 120, Type: sim.EvFlowWeight, FlowID: 1, Weight: 3}) // dynamic weight
	arrive(160, 31, 2, 4, 4)                                                     // flow 2 reactivates after an idle gap

	e.Run(until)

	fmt.Printf("service trace (rate 1 byte/tick, until tick %d)\n", until)
	fmt.Printf("%-4s %-4s %-7s %-4s %-6s %-5s %-9s %-9s\n",
		"from", "to", "packet", "flow", "tenant", "bytes", "vstart", "vfinish")
	for _, r := range e.Trace {
		fmt.Printf("%-4d %-4d %-7d %-4d %-6d %-5d %-9.3f %-9.3f\n",
			r.Start, r.End, r.PacketID, r.FlowID, r.TenantID, r.Bytes, r.VStart, r.VFinish)
	}
	fmt.Printf("\nstats: %+v\n", e.Stats)
	for _, fid := range []int{1, 2, 3} {
		fmt.Printf("flow %d: served %d bytes\n", fid, e.Sched.Flows[fid].ServedBytes)
	}

	// Independent fluid reference over the same admitted workload (the
	// cancelled packet 43 never enters either model).
	fluid := sim.FluidServe([]sim.FluidFlow{
		{FlowID: 1, TenantID: 1, Weight: 1, TenantWeight: 2,
			Arrivals:     []sim.FluidArrival{{Tick: 0, Bytes: 80}},
			WeightEvents: []sim.FluidWeightEvent{{Tick: 120, Weight: 3}}},
		{FlowID: 2, TenantID: 1, Weight: 1, TenantWeight: 2,
			Arrivals: []sim.FluidArrival{{Tick: 0, Bytes: 32}, {Tick: 160, Bytes: 16}}},
		{FlowID: 3, TenantID: 2, Weight: 1, TenantWeight: 1,
			Arrivals: []sim.FluidArrival{{Tick: 0, Bytes: 30}}},
	}, 1, until)

	rep := sim.ByteDeviation(e.Trace, fluid, until)
	fmt.Printf("\nbyte-level deviation vs ideal fluid scheduler\n")
	for _, fid := range []int{1, 2, 3} {
		fmt.Printf("flow %d: max deviation %.3f bytes\n", fid, rep.PerFlowMax[fid])
	}
	fmt.Printf("overall: max deviation %.3f bytes (flow %d @ tick %d)\n",
		rep.MaxBytes, rep.WorstFlow, rep.WorstTick)
}
