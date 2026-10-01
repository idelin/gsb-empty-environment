// Command demo runs a sample weighted-fair-queueing scenario in the
// discrete-event simulator, prints the service trace, and reports the
// byte-level deviation from an ideal fluid (GPS) scheduler.
package main

import (
	"fmt"
	"reflect"

	"wfqsim"
)

const (
	rate   = 100 // link rate, bytes per tick
	endAt  = 900
	maxLen = 400
)

func buildScenario() ([]wfqsim.Tenant, []wfqsim.Flow, []wfqsim.Event) {
	tenants := []wfqsim.Tenant{
		{ID: 1, Weight: 2},
		{ID: 2, Weight: 1},
	}
	flows := []wfqsim.Flow{
		{ID: 1, TenantID: 1, Weight: 1},
		{ID: 2, TenantID: 1, Weight: 1},
		{ID: 3, TenantID: 2, Weight: 1},
		{ID: 4, TenantID: 2, Weight: 1}, // idle until tick 300
	}
	var events []wfqsim.Event
	arrive := func(id, flow, tenant, length, tick int) {
		events = append(events, wfqsim.Event{
			Tick:   tick,
			Kind:   wfqsim.EvArrive,
			Packet: wfqsim.Packet{ID: id, FlowID: flow, TenantID: tenant, Len: length},
		})
	}
	for i := 0; i < 200; i++ { // flow 1: 100-byte packets, ~25 B/tick offered
		arrive(1000+i, 1, 1, 100, i*4)
	}
	for i := 0; i < 60; i++ { // flow 2: 400-byte packets, ~33 B/tick offered
		arrive(2000+i, 2, 1, 400, i*12)
	}
	for i := 0; i < 100; i++ { // flow 3: 200-byte packets, ~33 B/tick offered
		arrive(3000+i, 3, 2, 200, i*6)
	}
	for i := 0; i < 100; i++ { // flow 4: idle at first, reactivates at tick 300
		arrive(4000+i, 4, 2, 100, 300+i*4)
	}
	events = append(events,
		wfqsim.Event{Tick: 50, Kind: wfqsim.EvCancel, PacketID: 2005},
		wfqsim.Event{Tick: 150, Kind: wfqsim.EvPause},  // backpressure
		wfqsim.Event{Tick: 170, Kind: wfqsim.EvResume}, //
		wfqsim.Event{Tick: 400, Kind: wfqsim.EvWeightFlow, FlowID: 1, Weight: 2},
		wfqsim.Event{Tick: 600, Kind: wfqsim.EvWithdraw, FlowID: 3},
		wfqsim.Event{Tick: endAt, Kind: wfqsim.EvEnd},
	)
	for t := 200; t < endAt; t += 200 {
		events = append(events, wfqsim.Event{Tick: t, Kind: wfqsim.EvCheckpoint})
	}
	return tenants, flows, events
}

func main() {
	tenants, flows, events := buildScenario()

	sim := wfqsim.NewSim(rate, tenants, flows)
	gps := wfqsim.NewGPS(rate, tenants, flows)
	for _, ev := range events {
		sim.Add(ev)
		gps.Add(ev)
	}
	sim.Run()
	gps.Run()

	fmt.Printf("=== WFQ discrete-event demo (rate=%d B/tick, end=%d) ===\n", rate, endAt)
	fmt.Println("tenants: 1(w=2){flows 1,2}  2(w=1){flows 3,4}; flow1 weight 1->2 @400; flow3 withdrawn @600")
	fmt.Println("flow4 idle until @300; pause @150 resume @170; cancel pkt 2005 @50")
	fmt.Println()

	fmt.Println("--- service trace (first 25 completions) ---")
	for i, e := range sim.Trace {
		if i >= 25 {
			break
		}
		fmt.Printf("tick %4d  flow %d  pkt %4d  %d B\n", e.Tick, e.FlowID, e.PacketID, e.Bytes)
	}
	fmt.Printf("... %d completions total\n\n", len(sim.Trace))

	fmt.Println("--- per-flow served bytes (packet scheduler) ---")
	for _, d := range wfqsim.CompareServed(sim, gps) {
		fmt.Printf("flow %d: %d B\n", d.FlowID, d.Sched)
	}
	fmt.Println()

	fmt.Println("--- byte deviation vs ideal fluid (GPS) scheduler ---")
	devs := wfqsim.CompareServed(sim, gps)
	maxDev := 0.0
	for _, d := range devs {
		fmt.Printf("flow %d: sched=%8d B  gps=%9.1f B  diff=%+8.1f B\n", d.FlowID, d.Sched, d.GPS, d.Diff)
		if d.Diff > maxDev {
			maxDev = d.Diff
		}
		if -d.Diff > maxDev {
			maxDev = -d.Diff
		}
	}
	fmt.Printf("max |deviation| = %.1f B (max packet = %d B)\n\n", maxDev, maxLen)

	fmt.Println("--- checkpoint deviations (bytes, sched - gps) ---")
	for _, row := range sortedMarks(wfqsim.CompareMarks(sim, gps)) {
		fmt.Printf("tick %4d:", row.tick)
		for _, d := range row.devs {
			fmt.Printf("  f%d=%+.0f", d.FlowID, d.Diff)
		}
		fmt.Println()
	}
	fmt.Println()

	// Recoverability: rerun, checkpoint at tick 250, restore, continue.
	simA := wfqsim.NewSim(rate, tenants, flows)
	for _, ev := range events {
		simA.Add(ev)
	}
	simA.Run()
	simB := wfqsim.NewSim(rate, tenants, flows)
	for _, ev := range events {
		simB.Add(ev)
	}
	simB.RunUntil(250)
	blob, err := simB.Save()
	if err != nil {
		panic(err)
	}
	simC, err := wfqsim.Load(blob)
	if err != nil {
		panic(err)
	}
	simC.Run()
	fmt.Printf("--- recoverable event queue ---\n")
	fmt.Printf("checkpoint @%d -> %d bytes; restored trace identical to uninterrupted run: %v\n",
		250, len(blob), reflect.DeepEqual(simA.Trace, simC.Trace))
}

func sortedMarks(m map[int][]wfqsim.FlowDeviation) []markRow {
	rows := make([]markRow, 0, len(m))
	for tick, ds := range m {
		rows = append(rows, markRow{tick, ds})
	}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows); j++ {
			if rows[j].tick < rows[i].tick {
				rows[i], rows[j] = rows[j], rows[i]
			}
		}
	}
	return rows
}

type markRow struct {
	tick int
	devs []wfqsim.FlowDeviation
}
