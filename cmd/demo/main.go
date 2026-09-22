// Command demo runs a scripted scenario in the single-process event simulator
// and prints how the recyclable prefix evolves:
//
//  1. 3 replicas replicate entries while all are active.
//  2. replica 2 is partitioned (slow/offline): recycling is blocked.
//  3. it is explicitly removed (eviction records the snapshot re-entry rule):
//     recycling advances over the remaining active members.
//  4. it is wiped and readded: it re-enters via snapshot, never via deleted
//     log entries, catches the new tail, and is promoted to a full member.
package main

import (
	"fmt"

	"logrecycle/sim"
)

func main() {
	const n = 3
	s := sim.New(n, 42)
	sys := sim.NewSystem(s, n)
	sys.Logf = func(format string, a ...any) {
		fmt.Printf("    %s\n", fmt.Sprintf(format, a...))
	}

	snap := func() string {
		return fmt.Sprintf("leader{snap=%d last=%d} f1{snap=%d last=%d} f2{snap=%d last=%d, evicted=%v}",
			sys.DurableSnap(0), sys.DurableLast(0),
			sys.DurableSnap(1), sys.DurableLast(1),
			sys.DurableSnap(2), sys.DurableLast(2), sys.Evicted(2))
	}

	submit := func(k int) {
		for i := 0; i < k; i++ {
			sys.Submit(fmt.Sprintf("x-%02d", sys.DurableLast(0)+i+1))
		}
	}

	fmt.Println("== phase 1: all replicas healthy, submit 6 ==")
	submit(6)
	s.RunUntil(200, 0)
	fmt.Println("   ", snap())

	fmt.Println("== phase 2: partition replica 2, submit 4 more ==")
	s.Partition(2)
	submit(4)
	s.RunUntil(500, 0)
	fmt.Println("   ", snap())
	fmt.Printf("     recyclable prefix stays at %d (slow active replica blocks it)\n",
		sys.DurableSnap(0))

	fmt.Println("== phase 3: explicitly remove replica 2 ==")
	sys.Evict(2, s.Now())
	s.RunUntil(900, 0)
	fmt.Println("   ", snap())
	if rec, ok := sys.EvictRecordFor(2); ok {
		fmt.Printf("     eviction record: re-entry requires snapshot >= %d\n", rec.RequiredSnap)
	}

	fmt.Println("== phase 4: submit 3 tail entries, then wiped readd of replica 2 ==")
	submit(3)
	s.RunUntil(1100, 0)
	s.Heal(2)
	sys.Readd(2, s.Now())
	s.RunUntil(1800, 0)
	fmt.Println("   ", snap())

	fmt.Println("== convergence check ==")
	for i := 0; i < n; i++ {
		fmt.Printf("     node%d: durable last=%d hash=%016x\n",
			i, sys.DurableLast(i), sys.DurableHash(i))
	}
	if sys.DurableHash(0) == sys.DurableHash(1) && sys.DurableHash(1) == sys.DurableHash(2) {
		fmt.Println("     all state-machine hashes equal: CONVERGED")
	} else {
		fmt.Println("     hash mismatch!")
	}
}
