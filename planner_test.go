package main

import "testing"

func intPtr(v int) *int { return &v }

func mustSolve(t *testing.T, in Input) Plan {
	t.Helper()
	p, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if !p.Found {
		t.Fatalf("expected a plan, got none")
	}
	return p
}

// Earliest arrival wins; no swap needed.
func TestBasicEarliestArrival(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 10, Start: 0, Goal: 2,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 4, Time: 10},
			{From: 1, To: 2, Energy: 4, Time: 10},
			{From: 0, To: 2, Energy: 9, Time: 100},
		},
	})
	if p.TotalTime != 20 || p.TotalCost != 0 {
		t.Fatalf("got time=%d cost=%d, want 20/0", p.TotalTime, p.TotalCost)
	}
	// battery after the two rides: 10-4-4 = 2
	last := p.Steps[len(p.Steps)-1]
	if last.BatteryAfter != 2 {
		t.Fatalf("final battery %d, want 2", last.BatteryAfter)
	}
}

// Battery insufficient for the second leg: must swap at node 1.
func TestSwapRequired(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 5, Start: 0, Goal: 2,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 4, Time: 10},
			{From: 1, To: 2, Energy: 4, Time: 10},
		},
		Stations: []Station{{Node: 1, Fee: 7, SwapTime: 3, Open: 0, Close: 1440}},
	})
	if p.TotalTime != 23 || p.TotalCost != 7 {
		t.Fatalf("got time=%d cost=%d, want 23/7", p.TotalTime, p.TotalCost)
	}
	var sawSwap bool
	for _, s := range p.Steps {
		if s.Kind == "wait+swap" {
			sawSwap = true
			if s.BatteryAfter != 5 || s.Wait != 0 {
				t.Fatalf("swap step: battery=%d wait=%d, want 5/0", s.BatteryAfter, s.Wait)
			}
		}
	}
	if !sawSwap {
		t.Fatalf("expected a swap step in %+v", p.Steps)
	}
}

// Station closed on arrival: wait until 08:00 (480 min).
func TestWaitForOpening(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 5, Start: 0, Goal: 2,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 4, Time: 10},
			{From: 1, To: 2, Energy: 4, Time: 10},
		},
		Stations: []Station{{Node: 1, Fee: 7, SwapTime: 3, Open: 480, Close: 1200}},
	})
	// arrive node1 at t=10, wait 470, swap 3 -> 483, ride 10 -> 493
	if p.TotalTime != 493 || p.TotalCost != 7 {
		t.Fatalf("got time=%d cost=%d, want 493/7", p.TotalTime, p.TotalCost)
	}
	for _, s := range p.Steps {
		if s.Kind == "wait+swap" && s.Wait != 470 {
			t.Fatalf("wait=%d, want 470", s.Wait)
		}
	}
}

// Overnight window 20:00-08:00: arriving at 10:00 waits until 20:00.
func TestOvernightHours(t *testing.T) {
	if got := nextSwapStart(600, 1200, 480); got != 1200 {
		t.Fatalf("nextSwapStart(600, 1200, 480)=%d, want 1200", got)
	}
	if got := nextSwapStart(1300, 1200, 480); got != 1300 {
		t.Fatalf("inside window: got %d, want 1300", got)
	}
	if got := nextSwapStart(200, 1200, 480); got != 200 {
		t.Fatalf("inside overnight tail: got %d, want 200", got)
	}
	if got := nextSwapStart(900, 1200, 480); got != 1200 {
		t.Fatalf("gap between windows: got %d, want 1200", got)
	}
}

// Zero-energy (and one zero-time) cycles must not hang or change the answer.
func TestZeroEnergyCycleTerminates(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 10, Start: 0, Goal: 2,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 0, Time: 0}, // zero-energy zero-time edge
			{From: 1, To: 0, Energy: 0, Time: 0}, // ...forming a free cycle
			{From: 1, To: 1, Energy: 0, Time: 0}, // self loop
			{From: 0, To: 2, Energy: 3, Time: 7},
			{From: 1, To: 2, Energy: 3, Time: 5},
		},
	})
	if p.TotalTime != 5 || p.TotalCost != 0 {
		t.Fatalf("got time=%d cost=%d, want 5/0", p.TotalTime, p.TotalCost)
	}
}

// Same arrival time via two routes: the cheaper one must win.
func TestCostTieBreak(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 10, Start: 0, Goal: 3,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 5, Time: 10},
			{From: 1, To: 3, Energy: 5, Time: 10}, // needs swap at 1: arrive 20, cost 5
			{From: 0, To: 2, Energy: 10, Time: 20},
			{From: 2, To: 3, Energy: 0, Time: 0}, // arrive 20, cost 0
		},
		Stations: []Station{{Node: 1, Fee: 5, SwapTime: 0, Open: 0, Close: 1440}},
	})
	if p.TotalTime != 20 || p.TotalCost != 0 {
		t.Fatalf("got time=%d cost=%d, want 20/0", p.TotalTime, p.TotalCost)
	}
	for _, s := range p.Steps {
		if s.Kind == "wait+swap" {
			t.Fatalf("unexpected swap in cheapest plan: %+v", p.Steps)
		}
	}
}

// A slower-but-costlier label with more battery must NOT be dominated by a
// faster-cheaper label with less battery: only the swapped label can finish.
func TestDominanceKeepsHighBatteryCostlyState(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 10, InitialBattery: intPtr(6), Start: 0, Goal: 2,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 4, Time: 3}, // arrive node1: battery 2, t=3, cost 0
			{From: 1, To: 2, Energy: 5, Time: 1}, // needs 5 battery -> only after swap
			{From: 0, To: 2, Energy: 6, Time: 100},
		},
		Stations: []Station{{Node: 1, Fee: 10, SwapTime: 2, Open: 0, Close: 1440}},
	})
	// swap at 1: t=3+2=5, ride 1 -> t=6, cost 10 (beats direct t=100)
	if p.TotalTime != 6 || p.TotalCost != 10 {
		t.Fatalf("got time=%d cost=%d, want 6/10", p.TotalTime, p.TotalCost)
	}
}

// Unreachable goal: battery too small and no station on the way.
func TestUnreachable(t *testing.T) {
	p, err := Solve(Input{
		Capacity: 4, Start: 0, Goal: 1,
		Edges: []Edge{{From: 0, To: 1, Energy: 5, Time: 1}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Found {
		t.Fatalf("expected no plan, got %+v", p)
	}
}

// Two swaps on one trip; second swap is at a station that requires waiting.
func TestMultiSwapWithWait(t *testing.T) {
	p := mustSolve(t, Input{
		Capacity: 4, Start: 0, Goal: 3,
		Edges: []Edge{
			{From: 0, To: 1, Energy: 3, Time: 5},
			{From: 1, To: 2, Energy: 3, Time: 5},
			{From: 2, To: 3, Energy: 3, Time: 5},
		},
		Stations: []Station{
			{Node: 1, Fee: 2, SwapTime: 1, Open: 0, Close: 1440},
			{Node: 2, Fee: 3, SwapTime: 1, Open: 60, Close: 1440},
		},
	})
	// t=5 at node1, swap -> t=6 battery 4; ride -> t=11 battery 1 at node2;
	// wait until 60, swap -> t=61 battery 4; ride -> t=66. cost 5.
	if p.TotalTime != 66 || p.TotalCost != 5 {
		t.Fatalf("got time=%d cost=%d, want 66/5", p.TotalTime, p.TotalCost)
	}
}
