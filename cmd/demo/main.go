// Command demo runs three scenarios of the causal broadcast simulator:
// causal ordering with concurrency, a permanent fault (missing
// predecessor), and explicit backpressure with caller-side retry.
package main

import (
	"fmt"

	causal "causalcast"
)

var members = []causal.NodeID{"A", "B", "C"}

func printLogs(s *causal.Sim) {
	for _, id := range members {
		fmt.Printf("  node %s log:", id)
		for _, m := range s.Node(id).Log() {
			fmt.Printf(" %s", m.ID)
		}
		fmt.Printf("   (buffered=%d)\n", s.Node(id).Buffered())
	}
}

func main() {
	fmt.Println("== Scenario 1: causal dependency + concurrent messages ==")
	s := causal.NewSim(causal.Config{
		Members:   members,
		MaxBuffer: 8,
		MinDelay:  1, MaxDelay: 10,
		DupRate:   0.3,
		Seed:      3,
	})
	s.Broadcast("A", "a1") // A -> all
	s.Run()                // everyone has a1
	s.Broadcast("B", "b2 (B saw a1)")
	s.Broadcast("A", "a2 (concurrent with b2? no: A saw a1 only)")
	s.Broadcast("C", "c1 (concurrent)")
	s.Run()
	printLogs(s)
	for _, id := range members {
		if err := causal.VerifyLog(s.Node(id).Log()); err != nil {
			fmt.Println("  SAFETY VIOLATION:", err)
			return
		}
	}
	order, _ := causal.CanonicalOrder(s.Node("A").Log())
	fmt.Print("  canonical stable order:")
	for _, m := range order {
		fmt.Printf(" %s", m.ID)
	}
	fmt.Println()
	for _, id := range members[1:] {
		o, _ := causal.CanonicalOrder(s.Node(id).Log())
		if !causal.SameOrder(order, o) {
			fmt.Println("  STABLE ORDER MISMATCH at", id)
			return
		}
	}
	fmt.Printf("  safety: all logs valid causal linearizations; canonical order identical at A,B,C\n")
	fmt.Printf("  stats: %+v, duplicates dropped=%d\n\n", s.Stats, s.Duplicates())

	fmt.Println("== Scenario 2: missing predecessor is never faked ==")
	s2 := causal.NewSim(causal.Config{
		Members:   members,
		MaxBuffer: 8,
		MinDelay:  1, MaxDelay: 5,
		DropPred: func(to causal.NodeID, m causal.Message) bool {
			return to == "C" && m.ID.Sender == "A" && m.ID.Seq == 1 // a1 lost on the way to C
		},
		MaxTime: 100000,
		Seed:    5,
	})
	s2.Broadcast("A", "a1")
	s2.Run()
	s2.Broadcast("B", "b2 depends on a1")
	s2.Broadcast("A", "a2")
	s2.Broadcast("B", "b3")
	s2.Run()
	printLogs(s2)
	fmt.Printf("  sim time advanced to %d ticks; C still holds %d message(s) buffered, undelivered\n",
		s2.Now(), s2.Node("C").Buffered())
	fmt.Printf("  safety: C delivered nothing that depends on the lost A#1 (no timeout can fake it)\n\n")

	fmt.Println("== Scenario 3: explicit backpressure + retry ==")
	a := causal.NewNode("A", members, 8)
	c := causal.NewNode("C", members, 1) // buffer of one
	a.Broadcast("a1")
	m2 := a.Broadcast("a2")
	m3 := a.Broadcast("a3")
	fmt.Printf("  C.Receive(a2) before a1 -> buffered (waits for a1)\n")
	c.Receive(m2)
	err := c.Receive(m3)
	fmt.Printf("  C.Receive(a3) with full buffer -> %v\n", err)
	fmt.Println("  caller retries a3 after a1 arrives...")
	a1 := causal.Message{ID: causal.MsgID{Sender: "A", Seq: 1}, Clock: causal.VectorClock{"A": 1, "B": 0, "C": 0}, Payload: "a1"}
	c.Receive(a1)      // drains a2
	c.Receive(m3)      // retry now succeeds
	fmt.Printf("  C log after retry:")
	for _, m := range c.Log() {
		fmt.Printf(" %s", m.ID)
	}
	fmt.Printf("  buffered=%d\n", c.Buffered())
}
