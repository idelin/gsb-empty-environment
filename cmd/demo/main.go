package main

import (
	"fmt"

	"causalrouter"
)

func main() {
	fmt.Println("=== Scenario 1: causal ordering (acceptance boundary) ===")
	x := causalrouter.NewClient("A")
	y := causalrouter.NewClient("B")

	m1 := x.Send("hello from A")
	y.Receive(m1)
	m2 := y.Send("reply from B, causally after A:1")

	r := causalrouter.NewRouter()
	r.OnDeliver(func(m *causalrouter.Message) {
		fmt.Printf("  DELIVER  %s\n", m)
	})

	fmt.Printf("receiver gets Y's message first: %s\n", m2)
	_ = r.Submit(m2)
	fmt.Printf("  buffered, pending=%d delivered=%d (waiting for A:1)\n",
		len(r.Pending()), len(r.Delivered()))

	fmt.Printf("receiver gets X's message:      %s\n", m1)
	_ = r.Submit(m1)
	fmt.Printf("  after drain: pending=%d delivered=%d\n",
		len(r.Pending()), len(r.Delivered()))

	fmt.Println()
	fmt.Println("=== Scenario 2: causal cycle detection and quarantine ===")
	r2 := causalrouter.NewRouter()
	forgedC := causalrouter.NewMessage("C", causalrouter.VectorClock{"C": 1, "D": 1}, "claims to follow D:1")
	forgedD := causalrouter.NewMessage("D", causalrouter.VectorClock{"D": 1, "C": 1}, "claims to follow C:1")
	_ = r2.Submit(forgedC)
	_ = r2.Submit(forgedD)
	for _, rec := range r2.Quarantined() {
		fmt.Printf("  QUARANTINE %s\n  evidence: %s\n", rec.Message.ID, rec.Evidence)
	}
}
