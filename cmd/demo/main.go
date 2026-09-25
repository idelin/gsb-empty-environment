package main

import (
	"fmt"

	"causal"
)

func main() {
	fmt.Println("=== Scenario 1: causal broadcast under chaos faults (delay + duplicates) ===")
	sim := causal.NewSimulator(3, 16, causal.Chaos{MinDelay: 1, MaxDelay: 10, DupRate: 0.3}, 1)
	sim.RetryOnBackpressure = true
	sim.Send(0, 0, "A: hello")          // concurrent with C's first message
	sim.Send(0, 2, "C: hi there")       // concurrent with A's first message
	sim.Send(20, 1, "B: reply after A") // B has seen A's message by t=20
	sim.Send(25, 0, "A: follow-up")
	sim.Run()

	for _, r := range sim.Log {
		fmt.Println(r)
	}
	fmt.Println()

	if err := causal.VerifySafety(sim.Log); err != nil {
		fmt.Println("SAFETY: VIOLATION:", err)
	} else {
		fmt.Println("SAFETY: OK — no node delivered a message before its causal predecessor")
	}
	if err := causal.VerifyExactlyOnce(sim.Log); err != nil {
		fmt.Println("DEDUP: VIOLATION:", err)
	} else {
		fmt.Println("DEDUP: OK — every message delivered at most once per node")
	}

	fmt.Println()
	fmt.Println("Stable total order computed independently at each node:")
	for node := 0; node < 3; node++ {
		var msgs []causal.Message
		for _, r := range sim.Log {
			if r.Node == node {
				msgs = append(msgs, r.Msg)
			}
		}
		fmt.Printf("  n%d: %v\n", node, causal.StableOrder(msgs))
	}

	fmt.Println()
	fmt.Println("=== Scenario 2: permanent loss of a predecessor (n0#1 -> n2 dropped) ===")
	sim2 := causal.NewSimulator(3, 8, causal.Drop{
		Inner: causal.Reliable{Delay: 1},
		If:    func(m causal.Message, to int) bool { return m.ID == (causal.MsgID{Sender: 0, Seq: 1}) && to == 2 },
	}, 1)
	sim2.Send(0, 0, "lost-to-n2")
	sim2.Send(0, 0, "dependent-on-lost")
	sim2.Run()
	fmt.Printf("n2 clock after run: %v\n", []int(sim2.Nodes[2].Clock()))
	for _, line := range sim2.Nodes[2].PendingReport() {
		fmt.Println("n2 pending:", line)
	}
	fmt.Println("=> dependent message waits forever; no timeout fakes the missing predecessor")
}
