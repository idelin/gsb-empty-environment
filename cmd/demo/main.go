// Demo: a lagging replica installs a chunked snapshot and then the log
// tail, inside a single-process event simulator. Scenario A partitions
// a follower; scenario B corrupts snapshot chunks on the wire.
package main

import (
	"fmt"

	"snaprepl"
)

func scenarioA() {
	fmt.Println("=== Scenario A: partitioned follower catches up via chunked snapshot ===")
	c := snaprepl.NewCluster(7, 3)
	c.Trace = func(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }
	c.StartHeartbeat()
	c.Net.Blocked[2] = true
	fmt.Println("t=0: follower 2 partitioned; client writes begin")
	c.ClientWrites(25, 5)
	c.Sim.After(220, func() {
		fmt.Println("t=220: follower 2 rejoins the cluster")
		delete(c.Net.Blocked, 2)
	})
	end, ok := c.RunUntilConverged(5000, 220)
	fmt.Println("--- evidence ---")
	fmt.Printf("liveness: all replicas converged=%v at virtual t=%d\n", ok, end)
	fmt.Printf("safety: monitor check = %v (nil means every apply ordered, deduped, consistent)\n", c.Mon.CheckSafety())
	fmt.Printf("applies per node: %v, rollbacks: %d\n", c.Mon.Applies(), len(c.Mon.Rollbacks))
	fmt.Printf("final leader state: %s\n\n", c.Nodes[0].SM)
}

func scenarioB() {
	fmt.Println("=== Scenario B: corrupted snapshot chunks -> rollback -> recovery ===")
	c := snaprepl.NewCluster(11, 3)
	c.Trace = func(format string, args ...interface{}) { fmt.Printf(format+"\n", args...) }
	c.StartHeartbeat()
	c.Net.Blocked[2] = true
	c.ClientWrites(15, 5)
	c.Sim.After(160, func() {
		fmt.Println("t=160: follower 2 rejoins; network starts corrupting chunks")
		delete(c.Net.Blocked, 2)
		c.Net.CorruptRate = 0.6
	})
	c.Sim.After(400, func() {
		fmt.Println("t=400: corruption stops")
		c.Net.CorruptRate = 0
	})
	end, ok := c.RunUntilConverged(5000, 400)
	fmt.Println("--- evidence ---")
	fmt.Printf("liveness: converged=%v at virtual t=%d\n", ok, end)
	fmt.Printf("safety: monitor check = %v; digest-mismatch rollbacks observed: %d\n",
		c.Mon.CheckSafety(), len(c.Mon.Rollbacks))
	fmt.Printf("final leader state: %s\n", c.Nodes[0].SM)
}

func main() {
	scenarioA()
	scenarioB()
}
