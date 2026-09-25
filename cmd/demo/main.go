// Command demo runs a scripted Raft scenario inside the event simulator:
// election, client writes, a minority partition, a crash/restart, healing
// and catch-up, printing the full trace and the final committed state.
package main

import (
	"fmt"

	"raftdemo/raft"
)

func main() {
	sim := raft.NewSim(5, 7)
	sim.Verbose = true

	sim.Schedule(400, func() { sim.Propose("set x=1") })
	sim.Schedule(500, func() { sim.Propose("set y=2") })
	sim.Schedule(700, func() {
		sim.Isolate(3)
		sim.Isolate(4)
	})
	sim.Schedule(800, func() { sim.Propose("set z=3") }) // commits on the majority side
	sim.Schedule(1100, func() { sim.Crash(1) })
	sim.Schedule(1400, func() { sim.Restart(1) })
	sim.Schedule(1500, func() { sim.Heal() })
	sim.Schedule(1800, func() { sim.Propose("set w=4") })
	sim.Run(3500)

	fmt.Println("=== event trace ===")
	for _, line := range sim.Logs {
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("=== final state ===")
	for i := 0; i < 5; i++ {
		fmt.Printf("node %d: log=%v applied=%v\n", i, sim.NodeLog(i), sim.AppliedCommands(i))
	}
	fmt.Printf("safety violations: %d\n", len(sim.Violations))
}
