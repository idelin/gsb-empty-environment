// Command demo simulates the acceptance scenario: a Byzantine silent node
// accepts Ping but refuses to relay PingReq. The monitor's local trust
// scorer decays the offender's score from 100 down to 20, at which point
// the cluster cuts direct probing and declares the node isolated.
package main

import (
	"fmt"

	"byzguard/cluster"
	"byzguard/trust"
)

func main() {
	cfg := trust.DefaultConfig()
	c := cluster.New(cfg, map[trust.NodeID]cluster.Behavior{
		"M":  cluster.Honest,           // monitor node
		"H1": cluster.Honest,           // honest fallback relay
		"H2": cluster.Honest,           // honest bystander
		"B":  cluster.ByzantineDropper, // answers Ping, drops every PingReq
		"T":  cluster.Honest,           // probe target
	})

	fmt.Printf("policy: initial=%.0f threshold=%.0f penalty(PingReq refused)=%.0f\n\n",
		cfg.InitialScore, cfg.QuarantineThreshold, cfg.PenaltyPingReqRefuse)

	for round := 1; round <= 6; round++ {
		fmt.Printf("== round %d ==\n", round)
		before := len(c.Logs)

		via := trust.NodeID("B")
		if c.Node("M").IsIsolated("B") {
			via = "H1" // fall back to an honest relay once B is cut off
		}
		acked := c.IndirectProbe("M", via, "T")

		for _, line := range c.Logs[before:] {
			fmt.Println("  " + line)
		}
		fmt.Printf("  => indirect probe of T via %s acked=%v\n\n", via, acked)
	}

	m := c.Node("M")
	fmt.Println("== final state ==")
	fmt.Printf("M: trust(B)=%.0f quarantined=%v consecutive_violations=%d\n",
		m.Scorer().Score("B"), m.Scorer().IsQuarantined("B"), m.Scorer().ConsecutiveViolations("B"))
	for _, id := range []trust.NodeID{"H1", "H2"} {
		fmt.Printf("%s: isolated(B)=%v trust(B)=%.0f\n",
			id, c.Node(id).IsIsolated("B"), c.Node(id).Scorer().Score("B"))
	}
	fmt.Printf("direct probes M sent to B after isolation: %d (must stay 0)\n",
		m.ProbeAttempts("B"))
	probeResult := c.DirectProbe("M", "B")
	fmt.Printf("post-isolation DirectProbe(M,B): sent=%v (blocked locally), attempts=%d\n",
		probeResult, m.ProbeAttempts("B"))
	for _, ev := range m.Scorer().Events() {
		fmt.Println("event:", ev)
	}
}
