package main

import "fmt"

func run(title string, sim *Sim, ticks int) {
	fmt.Printf("=== %s ===\n", title)
	for i := 0; i < ticks && !sim.Deadlock; i++ {
		events := sim.Step()
		fmt.Printf("t=%02d  %s\n", sim.Tick-1, sim.Snapshot())
		for _, e := range events {
			fmt.Printf("      - %s\n", e)
		}
	}
	if sim.Deadlock {
		fmt.Printf("system deadlocked at t=%02d\n", sim.Tick-1)
	}
	fmt.Println()
}

func main() {
	// Scenario 1: line, slow downstream -> upstream blocks, then cascade unblocks.
	s1 := NewSim([]*Station{
		{Name: "S1", Cap: 1, Proc: 2, Next: 1, Link: 1},
		{Name: "S2", Cap: 1, Proc: 4, Next: -1, Link: 1},
	}, true, 0, 1)
	run("line: source -> S1(p2) -> S2(p4) -> sink", s1, 12)

	// Scenario 2: two-station cycle, both full -> deadlock.
	cyc := NewSim([]*Station{
		{Name: "A", Cap: 1, Proc: 1, Next: 1, Link: 1},
		{Name: "B", Cap: 1, Proc: 1, Next: 0, Link: 1},
	}, false, 0, 0)
	cyc.Stations[0].Slots[0] = Slot{State: Waiting}
	cyc.Stations[1].Slots[0] = Slot{State: Waiting}
	run("cycle A<->B, both occupied", cyc, 6)

	// Scenario 3: three-station cycle with one free slot -> parts rotate via cascades.
	ring := NewSim([]*Station{
		{Name: "A", Cap: 1, Proc: 1, Next: 1, Link: 1},
		{Name: "B", Cap: 1, Proc: 1, Next: 2, Link: 1},
		{Name: "C", Cap: 1, Proc: 1, Next: 0, Link: 1},
	}, false, 0, 0)
	ring.Stations[0].Slots[0] = Slot{State: Waiting}
	ring.Stations[1].Slots[0] = Slot{State: Waiting}
	run("ring A->B->C->A, one free slot", ring, 8)
}
