// Command demo runs a scripted OR-Set scenario on the event simulator and
// prints a narrated trace: concurrent add/remove, a network partition,
// premature-GC counterexample, causal GC, and final convergence.
package main

import (
	"fmt"
	"strings"

	"orsetlab/sim"
)

func main() {
	fmt.Println("=== OR-Set demo: 3 replicas, simulated network (seed 42) ===")
	fmt.Println()

	s := sim.New(42, "A", "B", "C")

	fmt.Println("-- phase 1: adds and gossip on a healthy cluster")
	s.Add(1, "A", "apple")
	s.Add(2, "B", "banana")
	s.GossipAll(3, 3)

	fmt.Println("-- phase 2: partition C away; concurrent add vs remove of \"apple\"")
	s.Partition(10, []string{"C"}, []string{"A", "B"})
	s.Remove(11, "A", "apple") // A removes apple, cannot see C's add below
	s.Add(12, "C", "apple")    // C concurrently re-adds apple (unobserved)
	s.GossipAll(13, 2)         // C's messages are dropped by the partition

	fmt.Println("-- phase 3: heal; gossip until quiescent; then causal GC")
	s.Heal(20)
	s.GossipEvery(21, 2, 10, 2)
	s.GCAll(45)
	s.GossipEvery(46, 2, 3, 1)
	s.GCAll(55)

	s.Run()
	for _, line := range s.Log {
		fmt.Println("   " + line)
	}

	fmt.Println()
	fmt.Println("-- final state")
	for _, id := range s.IDs() {
		r := s.Replicas[id]
		fmt.Printf("   %s: elements=%v tombstones=%d add-dots=%d\n",
			id, r.Elements(), r.TombCount(), r.AddDotCount())
	}
	fmt.Printf("   converged=%v same-elements=%v delivered=%d dropped=%d\n",
		s.Converged(), s.SameElements(), s.Delivered, s.Dropped)
	fmt.Println()
	fmt.Println("note: \"apple\" survives A's remove because C's concurrent add")
	fmt.Println("was never observed by A; the remove only killed A's own tag.")
	fmt.Println()

	fmt.Println("-- counterexample: premature tombstone drop (no causal check)")
	s2 := sim.New(1, "A", "B")
	s2.Add(1, "A", "x")
	s2.Send(2, "A", "B", 1) // B observes x
	s2.Remove(4, "A", "x")  // A removes x; B has not seen the remove
	s2.At(5, func() {
		n := s2.Replicas["A"].DropTombstonesUnsafe()
		s2.Notef("A drops %d tombstone(s) WITHOUT causal confirmation", n)
	})
	s2.Send(6, "B", "A", 1) // B's stale state reintroduces x at A
	s2.Run()
	for _, line := range s2.Log {
		fmt.Println("   " + line)
	}
	alive := s2.Replicas["A"].Contains("x")
	fmt.Printf("   => x alive at A after premature GC: %v (resurrection anomaly)\n", alive)
	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("same scenario WITH causal GC (tombstone kept until stable):")
	s3 := sim.New(1, "A", "B")
	s3.Add(1, "A", "x")
	s3.Send(2, "A", "B", 1)
	s3.Remove(4, "A", "x")
	s3.GC(5, "A") // too early: B hasn't seen the remove -> no-op
	s3.Send(6, "B", "A", 1)
	s3.GossipAll(8, 1) // B learns the remove; confirmations flow back
	s3.GCAll(12)
	s3.GossipAll(14, 1) // one more round so A also confirms stability
	s3.GCAll(18)
	s3.Run()
	for _, line := range s3.Log {
		fmt.Println("   " + line)
	}
	fmt.Printf("   => x alive anywhere: A=%v B=%v; tombstones left: A=%d B=%d\n",
		s3.Replicas["A"].Contains("x"), s3.Replicas["B"].Contains("x"),
		s3.Replicas["A"].TombCount(), s3.Replicas["B"].TombCount())
}
