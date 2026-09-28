// Command flowlb-demo simulates weighted ECMP-style flow assignment over
// parallel paths in a discrete-event model (integer ticks, no real network
// traffic), then removes and adds paths to show minimal flow migration.
package main

import (
	"flag"
	"fmt"

	"flowlb"
)

func runPhase(a *flowlb.Assigner, flows int, ticks int64, title string) {
	s := flowlb.NewSim(a)
	for i := 0; i < flows; i++ {
		s.AddFlow(fmt.Sprintf("flow-%05d", i), 0, ticks)
	}
	s.Run(ticks)

	fmt.Printf("== %s ==\n", title)
	fmt.Printf("%-14s %6s %6s %10s %8s %10s %8s %9s\n",
		"path", "weight", "rate", "delivered", "share", "bytes", "avgWait", "maxQueue")
	total := s.Generated
	for _, p := range a.Paths() {
		st := s.Stats(p.ID)
		share := 0.0
		if total > 0 {
			share = 100 * float64(st.Delivered) / float64(total)
		}
		fmt.Printf("%-14s %6d %6d %10d %7.2f%% %10d %7.2f %9d\n",
			p.ID, p.Weight, p.Rate, st.Delivered, share, st.Bytes, st.AvgWait(), st.MaxQueue)
	}
	fmt.Printf("generated=%d delivered=%d dropped=%d\n\n",
		s.Generated, total-s.Dropped-queuedLeft(s, a), s.Dropped)
}

func queuedLeft(s *flowlb.Sim, a *flowlb.Assigner) int {
	n := 0
	for _, p := range a.Paths() {
		n += s.Backlog(p.ID)
	}
	return n
}

func main() {
	flows := flag.Int("flows", 2000, "number of flows")
	ticks := flag.Int64("ticks", 500, "simulation length in ticks")
	flag.Parse()

	a := flowlb.NewAssigner()
	for _, p := range []flowlb.Path{
		{ID: "path-A", Weight: 3, Rate: 500},
		{ID: "path-B", Weight: 2, Rate: 320},
		{ID: "path-C", Weight: 2, Rate: 320},
		{ID: "path-D", Weight: 1, Rate: 170},
		{ID: "path-standby", Weight: 0, Rate: 100}, // weight 0: no new flows
	} {
		a.AddPath(p)
	}

	runPhase(a, *flows, *ticks, "baseline: 5 paths (weights 3:2:2:1:0)")

	migrated, dropped := a.RemovePath("path-A")
	fmt.Printf(">> remove path-A: migrated %d/%d flows (%.2f%%), dropped %d\n\n",
		migrated, a.TotalFlows(), 100*float64(migrated)/float64(a.TotalFlows()), dropped)
	runPhase(a, *flows, *ticks, "after removing path-A")

	moved := a.AddPath(flowlb.Path{ID: "path-E", Weight: 3, Rate: 500})
	fmt.Printf(">> add path-E (weight 3): migrated %d/%d flows (%.2f%%)\n\n",
		moved, a.TotalFlows(), 100*float64(moved)/float64(a.TotalFlows()))
	runPhase(a, *flows, *ticks, "after adding path-E")
}
