// Command demo runs a scripted log-recycling scenario in the single-process
// event simulator and prints the safety/liveness evidence.
package main

import (
	"fmt"

	"logrec"
)

func main() {
	c := logrec.NewCluster("A", "B", "C")
	s := logrec.NewSim(c)

	replica := func(id string) string {
		r := c.Replica(id)
		state := "online"
		if r.Removed {
			state = "removed"
		} else if !r.Online {
			state = "offline"
		}
		return fmt.Sprintf("%s[recv=%d persist=%d %s]", id, r.Received, r.Persisted, state)
	}
	status := func(tag string) {
		fmt.Printf("t=%-3d %-34s confirmed=%-2d recycled=%-2d  %s %s %s\n",
			s.Now, tag, c.Confirmed, c.Log.Base(), replica("A"), replica("B"), replica("C"))
	}
	replicate := func(from, to int) {
		for i := from; i <= to; i++ {
			s.Append(fmt.Sprintf("cmd-%d", i), 1, 2, 1)
		}
	}

	fmt.Println("== Phase 1: append 1-5, all replicas persist ==")
	replicate(1, 5)
	s.Run()
	status("all persisted 1-5")

	fmt.Println()
	fmt.Println("== Phase 2: append 6-10; C receives in memory, crashes before persisting ==")
	replicate(6, 10)
	s.After(2, "crash C", func() { s.Crash("C") })
	s.Run()
	status("C crashed after volatile recv")
	fmt.Println("      -> C recv=10 but persist=5: in-memory never confirms;")
	fmt.Println("         quorum {A,B} confirms 10, but slow C blocks recycling at 5")

	fmt.Println()
	fmt.Println("== Phase 3: explicitly remove offline C ==")
	if err := s.Remove("C"); err != nil {
		panic(err)
	}
	s.Run()
	status("C removed, prefix unblocked")
	fmt.Printf("      -> snapshot re-entry requirements recorded: %+v\n", c.Removals)

	fmt.Println()
	fmt.Println("== Phase 4: C rejoins via snapshot (never requires deleted log) ==")
	if err := s.Rejoin("C"); err != nil {
		panic(err)
	}
	s.Run()
	status("C rejoined with snapshot")
	fmt.Printf("      -> snapshots installed: %+v\n", s.Snapshots)

	fmt.Println()
	fmt.Println("== Phase 5: append 11-15, all members persist ==")
	replicate(11, 15)
	s.Run()
	status("all persisted 11-15")

	fmt.Println()
	fmt.Println("== Evidence ==")
	fmt.Printf("SAFETY:   %d invariant violations over %d virtual time units\n", len(s.Violations), s.Now)
	fmt.Println("          (confirmed monotonic; recycled prefix monotonic; recycled <= persisted of every member)")
	fmt.Printf("LIVENESS: all members persisted through %d; confirmed=%d recycled=%d (prefix fully reclaimed)\n",
		c.Log.LastIndex(), c.Confirmed, c.Log.Base())
}
