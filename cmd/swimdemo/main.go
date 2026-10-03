// Command swimdemo runs the deterministic two-node mutual-death race
// scenario end to end and prints the protocol log, the final
// membership tables and the virtual switch statistics.
package main

import (
	"fmt"

	"swimdsim/sim"
	"swimdsim/swim"
)

func main() {
	const steps = 10000

	s := sim.New()
	// Virtual switch: seed 42, no random loss, 1-3 ticks of jitter.
	sw := sim.NewSwitch(s, 42, 0.0, 1, 3)

	cfg := swim.DefaultConfig()
	cfg.AckTimeout = 8
	cfg.IndirectTimeout = 8

	ids := []string{"A", "B"}
	nodes := make(map[string]*swim.Node, len(ids))
	for _, id := range ids {
		n := swim.NewNode(s, sw, cfg, id, ids)
		n.Logf = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
		nodes[id] = n
	}

	s.After(100, func() {
		fmt.Printf("tick=%04d **** virtual switch: link A<->B PARTITIONED ****\n", s.Now())
		sw.Partition("A", "B")
	})
	s.After(400, func() {
		fmt.Printf("tick=%04d **** virtual switch: link A<->B HEALED ****\n", s.Now())
		sw.Heal("A", "B")
	})

	for i := 0; i < steps; i++ {
		s.Step()
	}

	fmt.Printf("\nfinal membership after %d deterministic clock steps:\n", s.Now())
	for _, id := range ids {
		fmt.Printf("  node %s sees: %v\n", id, nodes[id].Members())
	}
	fmt.Printf("\nswitch stats: sent=%d dropped=%d delivered=%d\n", sw.Sent, sw.Dropped, sw.Delivered)
}
