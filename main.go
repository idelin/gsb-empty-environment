// Command quotasim runs a scripted demo of the replicated quota simulator:
// offline deductions, entitlement transfers over a flaky network (drops,
// duplicates), a crash/restart, then network healing — printing the full
// trace plus the safety/liveness evidence at the end.
package main

import (
	"fmt"

	"quotasim/sim"
)

func main() {
	// Total quota 1000, partitioned into three disjoint replica shards.
	s := sim.New(1000, []int64{400, 300, 300}, 7)
	s.CheckInvariants = true
	s.Net.DropRate = 0.4
	s.Net.DupRate = 0.3

	at := func(t int64, do func(s *sim.Simulator)) {
		s.Schedule(t, sim.ActionEvent{Do: do})
	}

	at(1, func(s *sim.Simulator) { s.Deduct(0, 120) }) // offline deduction
	at(2, func(s *sim.Simulator) { s.Deduct(2, 500) }) // must be rejected: no overdraw
	at(3, func(s *sim.Simulator) { s.Transfer(0, 1, 100, "T-0001") })
	at(4, func(s *sim.Simulator) { s.Crash(1) }) // receiver down: messages lost
	at(10, func(s *sim.Simulator) { s.Restart(1) })
	at(12, func(s *sim.Simulator) { s.Transfer(2, 0, 50, "T-0002") })
	at(13, func(s *sim.Simulator) { s.Transfer(2, 0, 50, "T-0002") }) // request retry: no double debit
	at(15, func(s *sim.Simulator) { s.Deduct(1, 30) })
	at(30, func(s *sim.Simulator) {
		s.Net.Healed = true // fairness assumption for liveness
	})

	s.Run(200)

	fmt.Println("=== TRACE ===")
	for _, line := range s.LogLines {
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println("=== EVIDENCE ===")
	fmt.Println(s.Report())
	fmt.Println()
	fmt.Println("SAFETY: conservation local+inflight+consumed == total held after every event;",
		"no replica overdraw; every transfer ID credited at most once ->",
		len(s.Violations), "violations")
	fmt.Println("LIVENESS: after network heal, all transfers settled ->", s.Settled(),
		"(queue empty:", s.PendingEvents() == 0, ")")
}
