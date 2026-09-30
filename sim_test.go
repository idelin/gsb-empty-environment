package main

import (
	"testing"
)

func simNet(t *testing.T, links []Link, flows []Flow) *Network {
	t.Helper()
	n, err := BuildNetwork(&Input{Links: links, Flows: flows})
	if err != nil {
		t.Fatalf("BuildNetwork: %v", err)
	}
	return n
}

func flowStat(res SimResult, id string) FlowSimStat {
	for _, f := range res.Flows {
		if f.FlowID == id {
			return f
		}
	}
	return FlowSimStat{FlowID: id}
}

func linkStat(res SimResult, id string, dir int) *LinkSimStat {
	for i := range res.Links {
		if res.Links[i].LinkID == id && res.Links[i].Dir == dir {
			return &res.Links[i]
		}
	}
	return nil
}

func TestSimUnderCapacityDelivers(t *testing.T) {
	n := simNet(t,
		[]Link{{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate}},
		[]Flow{{ID: "F1", Src: "A", Dst: "B", Demand: 5, Path: []string{"AB"}}},
	)
	res := Simulate(n, "", nil, 200)
	f := flowStat(res, "F1")
	if f.Generated != 1000 {
		t.Fatalf("generated = %d, want 1000", f.Generated)
	}
	if !f.OK {
		t.Fatalf("flow should be OK, backlog=%d", f.Backlog)
	}
	if ls := linkStat(res, "AB", DirFwd); ls == nil || ls.Congested {
		t.Fatalf("link should not be congested: %+v", ls)
	}
}

func TestSimOverloadGrowsBacklog(t *testing.T) {
	n := simNet(t,
		[]Link{{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate}},
		[]Flow{
			{ID: "F1", Src: "A", Dst: "B", Demand: 6, Path: []string{"AB"}},
			{ID: "F2", Src: "A", Dst: "B", Demand: 6, Path: []string{"AB"}},
		},
	)
	res := Simulate(n, "", nil, 400)
	ls := linkStat(res, "AB", DirFwd)
	if ls == nil || !ls.Congested {
		t.Fatalf("expected congested link, got %+v", ls)
	}
	if ls.EndQueue <= ls.MidQueue {
		t.Fatalf("queue should keep growing: mid=%d end=%d", ls.MidQueue, ls.EndQueue)
	}
	for _, id := range []string{"F1", "F2"} {
		if f := flowStat(res, id); f.OK {
			t.Fatalf("flow %s should be starved, backlog=%d", id, f.Backlog)
		}
	}
}

func TestSimSharedBidirectionalCapacity(t *testing.T) {
	// Shared 10 pkt/tick across both directions; 6+6=12 offered -> congested.
	n := simNet(t,
		[]Link{{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeShared}},
		[]Flow{
			{ID: "F1", Src: "A", Dst: "B", Demand: 6, Path: []string{"AB"}},
			{ID: "F2", Src: "B", Dst: "A", Demand: 6, Path: []string{"AB"}},
		},
	)
	res := Simulate(n, "", nil, 400)
	total := 0
	for _, ls := range res.Links {
		total += ls.Served
	}
	if total > 10*400+2 {
		t.Fatalf("shared capacity violated: served %d > ~4000", total)
	}
	congested := 0
	for _, ls := range res.Links {
		if ls.Congested {
			congested++
		}
	}
	if congested == 0 {
		t.Fatal("expected congestion on shared link")
	}
}

func TestSimSeparateDirectionsIndependent(t *testing.T) {
	// Separate mode: 8 fwd + 8 rev each fit under their own 10 cap.
	n := simNet(t,
		[]Link{{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate}},
		[]Flow{
			{ID: "F1", Src: "A", Dst: "B", Demand: 8, Path: []string{"AB"}},
			{ID: "F2", Src: "B", Dst: "A", Demand: 8, Path: []string{"AB"}},
		},
	)
	res := Simulate(n, "", nil, 200)
	for _, id := range []string{"F1", "F2"} {
		if f := flowStat(res, id); !f.OK {
			t.Fatalf("flow %s should be OK in separate mode, backlog=%d", id, f.Backlog)
		}
	}
}

func TestSimFractionalDemandRate(t *testing.T) {
	n := simNet(t,
		[]Link{{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate}},
		[]Flow{{ID: "F1", Src: "A", Dst: "B", Demand: 2.5, Path: []string{"AB"}}},
	)
	res := Simulate(n, "", nil, 100)
	f := flowStat(res, "F1")
	if f.Generated != 250 {
		t.Fatalf("generated = %d, want 250 (2.5 pkt/tick average)", f.Generated)
	}
	if !f.OK {
		t.Fatalf("fractional-rate flow should be OK, backlog=%d", f.Backlog)
	}
}

func TestSimMultiHopDelivery(t *testing.T) {
	n := simNet(t,
		[]Link{
			{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate},
			{ID: "BC", From: "B", To: "C", Capacity: 10, Mode: ModeSeparate},
			{ID: "CD", From: "C", To: "D", Capacity: 10, Mode: ModeSeparate},
		},
		[]Flow{{ID: "F1", Src: "A", Dst: "D", Demand: 4, Path: []string{"AB", "BC", "CD"}}},
	)
	res := Simulate(n, "", nil, 200)
	f := flowStat(res, "F1")
	if f.Generated != 800 || !f.OK {
		t.Fatalf("multi-hop flow: generated=%d ok=%v backlog=%d", f.Generated, f.OK, f.Backlog)
	}
}

func TestSimUnreachableFlowIsFailureNotSuccess(t *testing.T) {
	n := simNet(t,
		[]Link{
			{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate},
			{ID: "BC", From: "B", To: "C", Capacity: 10, Mode: ModeSeparate},
		},
		[]Flow{
			{ID: "F1", Src: "A", Dst: "C", Demand: 4, Path: []string{"AB", "BC"}},
			{ID: "F2", Src: "A", Dst: "B", Demand: 4, Path: []string{"AB"}},
		},
	)
	failed := map[int]bool{}
	bc, _ := n.LinkIndex("BC")
	failed[bc] = true
	res := Simulate(n, "S1", failed, 100)
	f1 := flowStat(res, "F1")
	if !f1.Unreachable || f1.OK {
		t.Fatalf("F1 must be unreachable and not OK: %+v", f1)
	}
	if f1.Generated != 0 || f1.Delivered != 0 {
		t.Fatalf("unreachable flow must not generate traffic: %+v", f1)
	}
	f2 := flowStat(res, "F2")
	if f2.Unreachable || !f2.OK {
		t.Fatalf("F2 should survive and be OK: %+v", f2)
	}
}
