// Command demo runs a scripted multi-replica MVR scenario inside the
// single-process event simulator and prints each step.
package main

import (
	"fmt"
	"strings"

	"mvr"
)

func show(s *mvr.Sim, label string) {
	fmt.Printf("--- %s (t=%d) ---\n", label, s.Now())
	for _, n := range s.Nodes() {
		var parts []string
		for _, e := range n.Reg.Entries() {
			parts = append(parts, fmt.Sprintf("%q ver=%s wall=%d", e.Value, e.Ver, e.Wall))
		}
		fmt.Printf("  %s: [%s]\n", n.ID, strings.Join(parts, ", "))
	}
}

func main() {
	s := mvr.NewSim(42)
	a := s.AddNode("A", 0)
	b := s.AddNode("B", 0)
	c := s.AddNode("C", 3600) // C's wall clock runs one hour fast

	fmt.Println("== step 1: three concurrent writes; C has a huge wall clock ==")
	a.Write("banana")
	b.Write("apple")
	c.Write("cherry")
	s.Run()
	show(s, "all three concurrent values retained; big wall clock swallows nothing")

	fmt.Println()
	fmt.Println("== step 2: B reads the conflict set and explicitly resolves ==")
	b.Resolve("fruit-salad")
	s.Run()
	show(s, "resolve result propagated to every replica")

	fmt.Println()
	fmt.Println("== step 3: partition C away; both sides keep writing ==")
	s.Partition("A", "C")
	s.Partition("B", "C")
	a.Write("pie")
	c.Write("cake")
	s.Run()
	show(s, "partitioned: sides diverge, no value lost")

	fmt.Println()
	fmt.Println("== step 4: heal; one anti-entropy round converges everyone ==")
	s.HealAll()
	s.SyncAll()
	s.Run()
	show(s, "converged with both partition-era values retained")

	fmt.Println()
	fmt.Println("== step 5: A resolves the new conflict ==")
	a.Resolve("dessert")
	s.Run()
	show(s, "final state")
	fmt.Printf("converged: %v\n", s.Converged())
}
