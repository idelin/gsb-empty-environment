// Command demo simulates a Byzantine silent node being locally evicted by
// trust decay in a SWIM-style cluster.
package main

import (
	"fmt"

	"swimtrust"
)

func main() {
	c := swimtrust.NewCluster()
	c.AddNode("A", swimtrust.BehaviorHonest, true)
	c.AddNode("B", swimtrust.BehaviorSilentRelay, true) // Byzantine: swallows PingReq
	c.AddNode("C", swimtrust.BehaviorHonest, true)
	c.AddNode("D", swimtrust.BehaviorHonest, true)
	c.AddNode("T", swimtrust.BehaviorHonest, false) // up, but direct-Ping partitioned

	fmt.Println("== Phase 1: A probes T; B silently drops every PingReq ==")
	for i := 0; i < 9; i++ {
		c.Probe("A", "T")
		fmt.Printf("round %2d: A's trust in B = %3d  quarantined=%v\n",
			c.Round, c.Node("A").Scorer.Score("B"), c.Node("A").Scorer.IsQuarantined("B"))
		c.Tick()
	}

	fmt.Println("\n== Phase 2: C and D independently reach the same verdict ==")
	for _, obs := range []string{"C", "D"} {
		for i := 0; i < 8; i++ {
			c.Probe(obs, "T")
			c.Tick()
		}
	}

	fmt.Println("\n== Isolation announcements ==")
	for _, a := range c.QuarantineAnnouncements("B") {
		fmt.Println("  " + a)
	}

	fmt.Println("\n== Post-quarantine: A refuses to direct-probe B ==")
	out := c.Probe("A", "B")
	fmt.Printf("  direct probe attempted: %v\n", out.DirectProbed)

	fmt.Println("\n== A's trust ledger ==")
	for _, rec := range c.Node("A").Scorer.Snapshot() {
		fmt.Printf("  peer=%s score=%3d quarantined=%-5v violations=%d\n",
			rec.NodeID, rec.Score, rec.Quarantined, len(rec.Violations))
	}

	fmt.Println("\n== Full event log ==")
	fmt.Println(c.EventLog())
}
