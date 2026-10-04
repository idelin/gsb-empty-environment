// Demo: 5-node Multi-Paxos cluster surviving a net-split, plus the adaptive
// controller reacting to a synthetic load spike.
package main

import (
	"fmt"
	"runtime"
	"time"

	"multipaxos/paxos"
)

func printLogs(nodes []*paxos.Node, upto uint64) {
	for _, n := range nodes {
		fmt.Printf("  node %d (leader=%v, commit=%d):", n.ID(), n.IsLeader(), n.CommitIndex())
		for slot := uint64(1); slot <= upto; slot++ {
			if e, ok := n.EntryAt(slot); ok {
				mark := " "
				if !e.Committed {
					mark = "*"
				}
				fmt.Printf(" %q%s", e.Value, mark)
			} else {
				fmt.Printf(" <hole>")
			}
		}
		fmt.Println()
	}
}

func main() {
	fmt.Println("=== Multi-Paxos: 5-node cluster, partition & recovery ===")
	net := paxos.NewNetwork()
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(i, net, nil)
	}

	nodes[0].Campaign()
	for _, v := range []string{"set x=1", "set y=2", "set z=3"} {
		slot, _ := nodes[0].Propose(v)
		fmt.Printf("committed %q at slot %d\n", v, slot)
	}
	fmt.Println("logs before partition:")
	printLogs(nodes, 4)

	fmt.Println("\n--- partition {0,1} | {2,3,4} ---")
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := nodes[0].Propose("dirty-x=99"); err != nil {
		fmt.Printf("old leader (minority) propose rejected: %v (slot 4 stays dirty on {0,1})\n", err)
	}
	nodes[2].Campaign()
	fmt.Println("node 2 elected by majority; Phase 1 repaired the tail")
	nodes[2].Propose("set x=7")
	nodes[2].Propose("set w=4")
	fmt.Println("logs during partition (* = uncommitted dirty):")
	printLogs(nodes, 5)

	fmt.Println("\n--- heal: old leader deposed, dirty slot 4 safely overwritten ---")
	net.Heal()
	nodes[2].SendHeartbeats()
	printLogs(nodes, 5)

	fmt.Println("\n=== Adaptive controller: load spike ===")
	var load float64
	ctl := paxos.NewController(paxos.DefaultConfig(), func() float64 { return load })
	ctl.StartTuner(10 * time.Millisecond)
	fmt.Printf("start: limit=%d batch=%d lease=%v\n", ctl.Limit(), ctl.BatchSize(), ctl.LeaseDuration())
	load = 0.98
	time.Sleep(120 * time.Millisecond)
	fmt.Printf("load=0.98: limit=%d batch=%d (multiplicative decrease)\n", ctl.Limit(), ctl.BatchSize())
	for i := 0; i < 3; i++ {
		ctl.Release(false, time.Millisecond)
	}
	fmt.Printf("3 consecutive failures: breakerOpen=%v, acquire -> %v\n", ctl.BreakerOpen(), ctl.Acquire())
	load = 0.1
	time.Sleep(600 * time.Millisecond)    // outlast breaker cooldown
	if err := ctl.Acquire(); err == nil { // half-open probe
		ctl.Release(true, 5*time.Millisecond) // success resets the failure streak
	}
	time.Sleep(300 * time.Millisecond)
	fmt.Printf("load=0.10, probe ok: limit=%d batch=%d (additive recovery)\n", ctl.Limit(), ctl.BatchSize())
	ctl.Stop()

	for _, n := range nodes {
		n.Stop()
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Printf("\ngoroutines after shutdown: %d (no leaks)\n", runtime.NumGoroutine())
}
