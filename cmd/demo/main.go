// Command demo runs the acceptance scenario of the lease state machine:
//
//	tick 100: node 0 requests the lease
//	tick 105: majority confirms the lease
//	tick 139: network blackhole isolates node 0
//	tick 150: node 0 demotes itself to candidate (before lease expiry)
//	tick 155: node 1 is elected leader by majority
//	tick 156: the new leader rejects the old primary's write request
package main

import (
	"fmt"

	"lease"
)

func main() {
	cfg := lease.Config{
		RoundTrip:       5,
		LeaseDuration:   50,
		HeartbeatEvery:  10,
		StepDownTimeout: 15,
		ElectionTimeout: 20,
		MaxClockDrift:   2,
	}
	c := lease.NewCluster(3, cfg)
	c.Boot(99)

	for c.Tick < 165 {
		c.Step()
		switch c.Tick {
		case 100:
			c.AcquireLease(0)
		case 139:
			c.Isolate(0) // inject network blackhole
		case 156, 160:
			c.SendWrite(0, "set x=1") // old primary tries to write
		}
	}

	fmt.Println("=== event log (discrete ticks) ===")
	for _, e := range c.Events {
		fmt.Println(e)
	}

	l := c.Leader()
	fmt.Printf("\nfinal state: tick=%d leader=node %d (term %d)\n", c.Tick, l.ID, l.Term)
	fmt.Printf("node 0 role=%s canWrite=%v (fenced, cannot write)\n", c.Nodes[0].Role, c.CanWrite(0))
}
