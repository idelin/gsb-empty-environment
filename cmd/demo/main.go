// Command demo drives the simulator through a causal-consistency scenario:
// a B version that depends on an A version reaches a replica without A, and a
// slice request must block until the missing cause arrives.
package main

import (
	"fmt"

	"causalslice/causal"
	"causalslice/sim"
)

func main() {
	reg := causal.NewRegistry()
	s := sim.New(reg, []string{"r1", "r2"})
	s.Latency = 3
	s.Gossip = 10

	s.OnEvent = func(e sim.Event) {
		fmt.Printf("  t=%-4d %-14s %s\n", e.Time, e.Kind, e.Detail)
	}

	var a1 causal.Ref
	s.Schedule(10, func() { a1, _ = s.Write("r1", "A", nil) })
	// Before the partition, withhold A@1 from r2 while letting B@1 through,
	// so r2 observes the effect (B) without its cause (A).
	s.FilterPush = func(now int, from, to string, items []causal.Ref) []causal.Ref {
		if to != "r2" || now >= 45 {
			return items
		}
		kept := items[:0]
		for _, r := range items {
			if r == a1 {
				continue
			}
			kept = append(kept, r)
		}
		return kept
	}
	s.Schedule(20, func() { _, _ = s.Write("r1", "B", []causal.Ref{a1}) })
	s.Schedule(45, func() { s.Partition("r1", "r2") })

	var blocked causal.Selection
	s.Schedule(55, func() {
		blocked = s.Read("r2", causal.Request{ID: "demo-read", Latest: []string{"A", "B"}})
		fmt.Println("  ---- r2 one-shot read at t=55 ----")
		fmt.Printf("  selected VV : %s\n", blocked.Selected)
		fmt.Printf("  closure  VV : %s\n", blocked.Closure)
		for _, b := range blocked.Blocking {
			fmt.Printf("  block       : %s\n", b)
		}
	})

	var served causal.Selection
	s.Schedule(55, func() {
		s.Wait("r2", causal.Request{ID: "demo-wait", Latest: []string{"A", "B"}},
			func(sel causal.Selection) { served = sel })
	})
	s.Schedule(150, func() { s.Heal("r1", "r2") })

	s.RunUntil(220)

	fmt.Println("  ---- result after heal ----")
	fmt.Printf("  waiter complete: %v   served slice VV: %s\n", served.Complete, served.Closure)
	fmt.Printf("  one-shot read at t=55: complete=%v blocking=%d\n",
		blocked.Complete, len(blocked.Blocking))
}
