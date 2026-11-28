// Example: run a single BBRv2 flow over the deterministic shallow-buffer
// bottleneck and print the state-machine trajectory and final convergence.
package main

import (
	"fmt"
	"time"

	"prescreen.local/ps-93493e0dc5/bbr"
	"prescreen.local/ps-93493e0dc5/netsim"
)

func main() {
	link := netsim.DefaultLinkConfig()
	sim := netsim.New(link)

	cfg := bbr.DefaultConfig()
	cfg.CruiseDuration = 500 * time.Millisecond

	var states []string
	cfg.OnStateChange = func(_, n bbr.State) {
		states = append(states, n.String())
	}
	var phases []string
	cfg.OnPhaseChange = func(_, n bbr.ProbePhase) {
		phases = append(phases, n.String())
	}

	flow := sim.AddFlow(1, 0, cfg) // appRateBps <= 0: always backlogged
	sim.Run(int64(6 * time.Second))

	stats := sim.Stats()
	b := flow.BBR()
	throughputMbps := float64(stats.Flows[1].Delivered) * float64(link.MSS) * 8 / 6.0 / 1e6

	fmt.Printf("main states visited : %v\n", states)
	fmt.Printf("probe sub-phases    : %v\n", phases)
	fmt.Printf("final state/phase   : %s/%s\n", b.State(), b.Phase())
	fmt.Printf("cwnd/inflight       : %d/%d packets\n", b.Cwnd(), b.Inflight())
	fmt.Printf("inflight_hi/lo      : %d/%d packets\n", b.InflightHi(), b.InflightLo())
	fmt.Printf("bw_hi/bw_lo         : %d/%d B/s\n", b.BwHi(), b.BwLo())
	fmt.Printf("min_rtt             : %v\n", b.MinRTT())
	fmt.Printf("startup early exit  : %v (inflight_hi locked: %v)\n",
		b.ExitedStartupEarly, b.StartupHiLocked)
	fmt.Printf("throughput          : %.2f Mbit/s of %.2f Mbit/s link\n",
		throughputMbps, float64(link.RateBps)*8/1e6)
	fmt.Printf("queue drops / marks : %d / %d\n", stats.QueueDrops, stats.ECNMarks)
}
