package main

import "testing"

// conflictTables is the classic 2-destination gadget: updating n0 first
// loops traffic for n3 (n0 -> n1 -> n0), updating n1 first loops traffic
// for n2 (n1 -> n0 -> n1). Only a synchronized update of {n0, n1} is safe.
func conflictTables() Tables {
	return Tables{
		N: 4,
		Old: [][]int{
			{0, 1, 1, 3},
			{0, 1, 2, 0},
			{0, 1, 2, 3},
			{0, 1, 2, 3},
		},
		New: [][]int{
			{0, 1, 2, 1},
			{0, 1, 0, 3},
			{0, 1, 2, 3},
			{0, 1, 2, 3},
		},
	}
}

func TestConflictRequiresSynchronization(t *testing.T) {
	p, err := NewPlanner(conflictTables())
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := p.Check(0); !ok {
		t.Fatal("old config should be safe")
	}
	if ok, _ := p.Check(p.fullMask()); !ok {
		t.Fatal("new config should be safe")
	}
	// Each single first update must be unsafe, with a detected loop.
	for _, v := range []int{0, 1} {
		safe, ev := p.Check(uint32(1) << uint(v))
		if safe {
			t.Fatalf("updating n%d first should be unsafe", v)
		}
		loops := 0
		for _, de := range ev.Dests {
			loops += len(de.Loops)
		}
		if loops == 0 {
			t.Fatalf("updating n%d first: expected loop evidence", v)
		}
	}
	if _, _, ok := p.FindOrder(); ok {
		t.Fatal("no safe per-node order should exist")
	}
	plan, ok := p.FindPlan()
	if !ok {
		t.Fatal("a group plan should exist")
	}
	if plan.MaxSize != 2 {
		t.Fatalf("min max group size = %d, want 2", plan.MaxSize)
	}
	if len(plan.Groups) != 1 || len(plan.Groups[0]) != 2 {
		t.Fatalf("groups = %v, want a single group {n0,n1}", plan.Groups)
	}
	assertPlanSafe(t, p, plan)
}

func TestPerNodeOrderExists(t *testing.T) {
	// Chain 0->1->2->3; new routes shortcut via n2 / direct.
	tab := Tables{
		N: 4,
		Old: [][]int{
			{0, 1, 1, 1},
			{0, 1, 2, 2},
			{0, 1, 2, 3},
			{0, 1, 2, 3},
		},
		New: [][]int{
			{0, 1, 2, 2},
			{0, 1, 2, 2},
			{0, 1, 2, 3},
			{0, 1, 2, 3},
		},
	}
	p, err := NewPlanner(tab)
	if err != nil {
		t.Fatal(err)
	}
	order, states, ok := p.FindOrder()
	if !ok {
		t.Fatal("expected a safe per-node order")
	}
	if len(order) != len(p.diff) {
		t.Fatalf("order %v does not cover diff %v", order, p.diff)
	}
	seen := map[int]bool{}
	for i, v := range order {
		if seen[v] {
			t.Fatalf("node %d updated twice", v)
		}
		seen[v] = true
		if safe, _ := p.Check(states[i]); !safe {
			t.Fatalf("state after step %d (update n%d) is unsafe", i+1, v)
		}
	}
	plan, ok := p.FindPlan()
	if !ok || plan.MaxSize != 1 {
		t.Fatalf("FindPlan = %+v, %v; want MaxSize 1", plan, ok)
	}
}

func TestBlackholeDetection(t *testing.T) {
	tab := Tables{
		N: 3,
		Old: [][]int{
			{0, 1, 1},
			{0, 1, 2},
			{0, 1, 2},
		},
		New: [][]int{
			{0, -1, 1}, // n0 loses its route to n1
			{0, 1, 2},
			{0, 1, 2},
		},
	}
	p, err := NewPlanner(tab)
	if err != nil {
		t.Fatal(err)
	}
	safe, ev := p.Check(p.fullMask())
	if safe {
		t.Fatal("all-new state should be unsafe (blackhole)")
	}
	found := false
	for _, de := range ev.Dests {
		if len(de.Stuck) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected blackhole evidence")
	}
}

func TestIdenticalTables(t *testing.T) {
	tab := conflictTables()
	tab.New = tab.Old
	p, err := NewPlanner(tab)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.diff) != 0 {
		t.Fatalf("diff = %v, want empty", p.diff)
	}
	plan, ok := p.FindPlan()
	if !ok || len(plan.Groups) != 0 {
		t.Fatalf("plan = %+v, want empty", plan)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewPlanner(Tables{N: 19}); err == nil {
		t.Fatal("expected error for >18 nodes")
	}
	bad := conflictTables()
	bad.Old[0][1] = 7
	if _, err := NewPlanner(bad); err == nil {
		t.Fatal("expected error for out-of-range next hop")
	}
}

// assertPlanSafe replays the plan and verifies every intermediate state.
func assertPlanSafe(t *testing.T, p *Planner, plan Plan) {
	t.Helper()
	mask := uint32(0)
	for i, g := range plan.Groups {
		for _, v := range g {
			mask |= uint32(1) << uint(v)
		}
		if mask != plan.States[i] {
			t.Fatalf("state mismatch at step %d", i+1)
		}
		if safe, _ := p.Check(mask); !safe {
			t.Fatalf("state after step %d is unsafe", i+1)
		}
	}
	if mask != p.needMask() {
		t.Fatal("plan does not update all changed nodes")
	}
}
