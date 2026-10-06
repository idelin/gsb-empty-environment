package main

import "testing"

func runSilent(sys *System, maxTicks int) (string, int) {
	for t := 0; t < maxTicks; t++ {
		sys.Events = nil
		sys.Tick(t)
		if s := sys.Status(); s != "RUNNING" {
			return s, t
		}
	}
	return "RUNNING", maxTicks
}

func machineOf(sys *System, si int) *Machine { return sys.Stations[si].Machines[0] }

// The slow middle station must block S1, and once S2 completes the chain
// must unblock in one tick: S2's job leaves, S1's blocked job follows, and
// a queued job enters S1 -- all in the same departure/arrival sequence.
func TestCascadeUnblocking(t *testing.T) {
	sys := scenarioCascade()

	// Advance until J2 blocks at S1 behind the busy S2.
	blockTick := -1
	for tick := 0; tick < 40; tick++ {
		sys.Events = nil
		sys.Tick(tick)
		if m := machineOf(sys, 0); m.State == Blocked && m.Job.ID == "J2" {
			blockTick = tick
			break
		}
	}
	if blockTick < 0 {
		t.Fatal("J2 never blocked at S1")
	}

	// Keep ticking; the tick in which J2 finally leaves S1 must be the same
	// tick in which S2 finishes J1 (cascade within one departure phase).
	for tick := blockTick + 1; tick < 40; tick++ {
		sys.Events = nil
		sys.Tick(tick)
		movedJ1, movedJ2 := false, false
		for _, e := range sys.Events {
			if e == "J1: S2 -> S3" {
				movedJ1 = true
			}
			if e == "J2: S1 -> S2" {
				movedJ2 = true
			}
		}
		if movedJ2 {
			if !movedJ1 {
				t.Fatalf("tick %d: J2 left S1 without J1 leaving S2 first", tick)
			}
			return
		}
	}
	t.Fatal("cascade never happened")
}

// Every job must eventually exit the line scenario.
func TestLineDrains(t *testing.T) {
	sys := scenarioCascade()
	status, _ := runSilent(sys, 100)
	if status != "DONE" {
		t.Fatalf("status = %s, want DONE", status)
	}
	if len(sys.Exited) != 4 {
		t.Fatalf("exited = %d, want 4", len(sys.Exited))
	}
}

// The cyclic configuration must be detected as a deadlock: jobs remain in
// the system but no machine is busy, so nothing can ever move again.
func TestDeadlock(t *testing.T) {
	sys := scenarioDeadlock()
	status, tick := runSilent(sys, 20)
	if status != "DEADLOCK" {
		t.Fatalf("status = %s, want DEADLOCK", status)
	}
	if len(sys.Exited) != 0 {
		t.Fatalf("exited = %d, want 0", len(sys.Exited))
	}
	// Both machines must be blocked, each holding its job.
	for si := 0; si < 2; si++ {
		if m := machineOf(sys, si); m.State != Blocked {
			t.Fatalf("tick %d: S%d machine state = %d, want Blocked", tick, si+1, m.State)
		}
	}
}

// Departure-before-arrival order: a machine freed in the departure phase
// must accept a new arrival in the same tick.
func TestDepartBeforeArrive(t *testing.T) {
	sys := &System{
		Stations:  []*Station{NewStation("S1", 1, 1)},
		Arrivals:  map[int][]*Job{0: {{ID: "J1", Route: []int{0}}}, 1: {{ID: "J2", Route: []int{0}}}},
	}
	sys.Tick(0) // J1 loads and starts
	sys.Tick(1) // J1 departs, J2 must enter in the same tick
	if len(sys.Exited) != 1 || sys.Exited[0].ID != "J1" {
		t.Fatalf("J1 should have exited, got %+v", sys.Exited)
	}
	if m := machineOf(sys, 0); m.Job == nil || m.Job.ID != "J2" {
		t.Fatalf("J2 should occupy S1 after tick 1, got %v", m)
	}
}
