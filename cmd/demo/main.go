// Command demo runs a scripted OR-Set scenario on the event simulator:
// concurrent add/remove across a partition, duplicate removes, crash and
// recovery, convergence, and causally-confirmed tombstone GC.
package main

import (
	"fmt"

	"orsetdemo/sim"
)

func main() {
	s := sim.New(1, "A", "B", "C")
	s.Verbose = true

	s.At(1, func(s *sim.Sim) { s.Nodes["A"].Set.Add("apple") })
	s.At(2, func(s *sim.Sim) { s.Nodes["B"].Set.Add("banana") })
	s.At(3, func(s *sim.Sim) { s.Gossip() })
	s.At(5, func(s *sim.Sim) { s.Gossip() })
	s.At(7, func(s *sim.Sim) {
		fmt.Printf("[t=%03d] state  A=%v B=%v C=%v\n",
			s.Now, s.Elements("A"), s.Elements("B"), s.Elements("C"))
	})

	// Partition A off; concurrent ops on both sides.
	s.At(8, func(s *sim.Sim) { s.Partition("A", "B"); s.Partition("A", "C") })
	s.At(9, func(s *sim.Sim) { s.Nodes["A"].Set.Remove("apple") }) // observed remove
	s.At(9, func(s *sim.Sim) { s.Nodes["B"].Set.Add("apple") })    // concurrent add
	s.At(10, func(s *sim.Sim) { s.Nodes["C"].Set.Remove("banana") })
	s.At(11, func(s *sim.Sim) { s.Nodes["C"].Set.Remove("banana") }) // duplicate: no-op
	s.At(12, func(s *sim.Sim) { s.Gossip() })                        // B<->C only
	s.At(14, func(s *sim.Sim) {
		fmt.Printf("[t=%03d] during partition A=%v B=%v C=%v\n",
			s.Now, s.Elements("A"), s.Elements("B"), s.Elements("C"))
	})

	// Crash C briefly, heal the partition, recover C.
	s.At(15, func(s *sim.Sim) { s.Crash("C") })
	s.At(16, func(s *sim.Sim) { s.Heal("A", "B"); s.Heal("A", "C") })
	s.At(17, func(s *sim.Sim) { s.Gossip() })
	s.At(19, func(s *sim.Sim) { s.Recover("C") })
	for t := uint64(20); t <= 30; t += 2 {
		s.At(t, func(s *sim.Sim) { s.Gossip() })
	}
	s.At(31, func(s *sim.Sim) {
		fmt.Printf("[t=%03d] healed  A=%v B=%v C=%v converged=%v\n",
			s.Now, s.Elements("A"), s.Elements("B"), s.Elements("C"), s.Converged())
	})

	// Causally-confirmed GC, then more gossip.
	s.At(32, func(s *sim.Sim) {
		for _, id := range []string{"A", "B", "C"} {
			n := s.Nodes[id].Set.GC()
			fmt.Printf("[t=%03d] gc %s: collected %d tombstone(s)\n", s.Now, id, n)
		}
	})
	s.At(33, func(s *sim.Sim) { s.Gossip() })
	s.At(35, func(s *sim.Sim) { s.Gossip() })
	s.At(36, func(s *sim.Sim) {
		for _, id := range []string{"A", "B", "C"} {
			s.Nodes[id].Set.GC()
		}
		fmt.Printf("[t=%03d] final   A=%v B=%v C=%v converged=%v tombstones A=%d B=%d C=%d\n",
			s.Now, s.Elements("A"), s.Elements("B"), s.Elements("C"), s.Converged(),
			s.Nodes["A"].Set.Tombstones(), s.Nodes["B"].Set.Tombstones(), s.Nodes["C"].Set.Tombstones())
	})

	s.Run(36)

	fmt.Println("--- event log ---")
	for _, line := range s.Log {
		fmt.Println(line)
	}
}
