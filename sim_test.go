package main

import "testing"

// Hand-computed timeline for source -> S1(p2) -> S2(p4) -> sink, all links 1 tick.
// Blocking appears at t=6..7 (S1 finished, S2 busy), cascade unblocks at t=8
// (S2->sink, then S1->S2, then source->S1, all in one tick).
func TestLineBlockingAndCascade(t *testing.T) {
	sim := NewSim([]*Station{
		{Name: "S1", Cap: 1, Proc: 2, Next: 1, Link: 1},
		{Name: "S2", Cap: 1, Proc: 4, Next: -1, Link: 1},
	}, true, 0, 1)

	want := []string{
		"S1[.] S2[.] transit{->S1@1}",        // t0: source injects
		"S1[P1] S2[.]",                       // t1: p1 arrives, starts
		"S1[P0] S2[.]",                       // t2
		"S1[.] S2[.] transit{->S2@4 ->S1@4}", // t3: p1->S2, source refills
		"S1[P1] S2[P3]",                      // t4
		"S1[P0] S2[P2]",                      // t5
		"S1[B] S2[P1]",                       // t6: p2 done, S2 busy -> blocked
		"S1[B] S2[P0]",                       // t7: still blocked
		"S1[.] S2[.] transit{->S2@9 ->S1@9}", // t8: cascade unblocks
		"S1[P1] S2[P3]",                      // t9
	}
	for tick, w := range want {
		sim.Step()
		if got := sim.Snapshot(); got != w {
			t.Errorf("t=%d: got  %s\nwant %s", tick, got, w)
		}
		if sim.Deadlock {
			t.Fatalf("t=%d: unexpected deadlock", tick)
		}
	}
}

// Two-station cycle, both occupied: after both finish at t=1 neither can
// depart -> deadlock detected at t=1.
func TestCycleDeadlock(t *testing.T) {
	sim := NewSim([]*Station{
		{Name: "A", Cap: 1, Proc: 1, Next: 1, Link: 1},
		{Name: "B", Cap: 1, Proc: 1, Next: 0, Link: 1},
	}, false, 0, 0)
	sim.Stations[0].Slots[0] = Slot{State: Waiting}
	sim.Stations[1].Slots[0] = Slot{State: Waiting}

	sim.Step()
	if got, want := sim.Snapshot(), "A[P0] B[P0]"; got != want {
		t.Fatalf("t=0: got %s want %s", got, want)
	}
	if sim.Deadlock {
		t.Fatal("t=0: premature deadlock")
	}
	sim.Step()
	if got, want := sim.Snapshot(), "A[B] B[B]"; got != want {
		t.Fatalf("t=1: got %s want %s", got, want)
	}
	if !sim.Deadlock {
		t.Fatal("t=1: deadlock not detected")
	}
}

// Three-station ring with one free slot: parts keep rotating; each tick's
// departures cascade (one frees a slot, the next uses it) and no deadlock occurs.
func TestRingCascadeNoDeadlock(t *testing.T) {
	sim := NewSim([]*Station{
		{Name: "A", Cap: 1, Proc: 1, Next: 1, Link: 1},
		{Name: "B", Cap: 1, Proc: 1, Next: 2, Link: 1},
		{Name: "C", Cap: 1, Proc: 1, Next: 0, Link: 1},
	}, false, 0, 0)
	sim.Stations[0].Slots[0] = Slot{State: Waiting}
	sim.Stations[1].Slots[0] = Slot{State: Waiting}

	want := []string{
		"A[P0] B[P0] C[.]",
		"A[.] B[.] C[.] transit{->C@2 ->B@2}",
		"A[.] B[P0] C[P0]",
		"A[.] B[.] C[.] transit{->A@4 ->C@4}",
		"A[P0] B[.] C[P0]",
		"A[.] B[.] C[.] transit{->B@6 ->A@6}",
	}
	for tick, w := range want {
		sim.Step()
		if got := sim.Snapshot(); got != w {
			t.Errorf("t=%d: got  %s\nwant %s", tick, got, w)
		}
		if sim.Deadlock {
			t.Fatalf("t=%d: unexpected deadlock", tick)
		}
	}
}
