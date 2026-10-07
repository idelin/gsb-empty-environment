package netsim

import (
	"strings"
	"testing"
)

func mustAddEdge(t *testing.T, n *Network, from, to, cap int) {
	t.Helper()
	if _, err := n.AddEdge(from, to, cap); err != nil {
		t.Fatalf("AddEdge(%d,%d): %v", from, to, err)
	}
}

func mustRoute(t *testing.T, n *Network, node, dst, next int) {
	t.Helper()
	if err := n.SetRoute(node, dst, next); err != nil {
		t.Fatalf("SetRoute(%d,%d,%d): %v", node, dst, next, err)
	}
}

// diamond: 0->1->3 and 0->2->3, traffic to 3 routed via 1, all caps 1.
func diamondSim(t *testing.T) *Sim {
	t.Helper()
	net := NewNetwork(4)
	mustAddEdge(t, net, 0, 1, 1)
	mustAddEdge(t, net, 0, 2, 1)
	mustAddEdge(t, net, 1, 3, 1)
	mustAddEdge(t, net, 2, 3, 1)
	mustRoute(t, net, 0, 3, 1)
	mustRoute(t, net, 1, 3, 3)
	mustRoute(t, net, 2, 3, 3)
	sim, err := NewSim(net, []*Packet{
		{ID: 0, Src: 0, Dst: 3, InjectTick: 0},
		{ID: 1, Src: 0, Dst: 3, InjectTick: 1},
		{ID: 2, Src: 0, Dst: 3, InjectTick: 2},
		{ID: 3, Src: 2, Dst: 3, InjectTick: 0},
	})
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	return sim
}

// ring: 0->1->2->0, cap 1, one packet per node sent two hops away.
func ringSim(t *testing.T, packets int) *Sim {
	t.Helper()
	net := NewNetwork(3)
	mustAddEdge(t, net, 0, 1, 1)
	mustAddEdge(t, net, 1, 2, 1)
	mustAddEdge(t, net, 2, 0, 1)
	mustRoute(t, net, 0, 1, 1)
	mustRoute(t, net, 0, 2, 1)
	mustRoute(t, net, 1, 0, 2)
	mustRoute(t, net, 1, 2, 2)
	mustRoute(t, net, 2, 0, 0)
	mustRoute(t, net, 2, 1, 0)
	var pkts []*Packet
	for i := 0; i < packets; i++ {
		pkts = append(pkts, &Packet{ID: i, Src: i, Dst: (i + 2) % 3})
	}
	sim, err := NewSim(net, pkts)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	return sim
}

func TestHealthyLatency(t *testing.T) {
	sim := diamondSim(t)
	res := sim.Run(sim.Initial(), 1000, nil)
	if !res.Done {
		t.Fatalf("not done: deadlock=%v\n%s", res.Deadlock, sim.Describe(res.Final))
	}
	want := map[int]int{0: 2, 1: 3, 2: 4, 3: 1}
	for id, w := range want {
		got, ok := sim.Latency(res.Final, id)
		if !ok || got != w {
			t.Errorf("packet %d latency = %d, %v; want %d", id, got, ok, w)
		}
	}
}

// The committed state must not depend on the node traversal order.
func TestNodeOrderIndependence(t *testing.T) {
	orders := [][]int{
		{0, 1, 2, 3},
		{3, 2, 1, 0},
		{2, 0, 3, 1},
	}
	var ref string
	for _, order := range orders {
		sim := diamondSim(t)
		st := sim.Initial()
		var trace strings.Builder
		for i := 0; i < 20; i++ {
			st = sim.Receive(st)
			trace.WriteString(sim.Hash(st))
			trace.WriteString("\n")
			cands := sim.Candidates(st)
			if len(cands) == 0 {
				break
			}
			st = sim.Apply(st, sim.Greedy(st, cands, order))
		}
		if ref == "" {
			ref = trace.String()
		} else if trace.String() != ref {
			t.Fatalf("order %v produced a different trace", order)
		}
	}
}

// Packets are never lost or duplicated, and capacities are never
// exceeded, at every tick of a run with blocking sends.
func TestConservationDuringRun(t *testing.T) {
	sim := diamondSim(t)
	st := sim.Initial()
	for i := 0; i < 20; i++ {
		st = sim.Receive(st)
		if errs := sim.CheckInvariants(st); len(errs) > 0 {
			t.Fatalf("tick %d: %v", st.Tick, errs)
		}
		cands := sim.Candidates(st)
		if len(cands) == 0 {
			break
		}
		st = sim.Apply(st, sim.Greedy(st, cands, nil))
	}
	if len(st.Delivered) != len(sim.Packets) {
		t.Fatalf("delivered %d of %d", len(st.Delivered), len(sim.Packets))
	}
}

func TestDeadlockRing(t *testing.T) {
	sim := ringSim(t, 3)
	res := sim.Run(sim.Initial(), 1000, nil)
	if !res.Deadlock {
		t.Fatalf("expected deadlock, got done=%v", res.Done)
	}
	ev := sim.DeadlockEvidence(res.Final)
	for _, want := range []string{
		"deadlock at tick 1: 3 of 3 packets undelivered",
		"wait-for cycle:",
		"cycle: 1 -> 2 -> 0 -> 1",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence missing %q:\n%s", want, ev)
		}
	}
}

// With only 2 packets the same ring never deadlocks: enumeration proves
// every maximal schedule delivers everything, and reports latency bounds.
func TestEnumerateRingNoDeadlock(t *testing.T) {
	sim := ringSim(t, 2)
	rep := sim.Enumerate(100000)
	if rep.Overflow {
		t.Fatal("state space overflow")
	}
	if len(rep.InvariantErrors) > 0 {
		t.Fatalf("invariant violations: %v", rep.InvariantErrors)
	}
	if rep.DeadlockTerminals != 0 {
		t.Fatalf("unexpected %d deadlocks", rep.DeadlockTerminals)
	}
	if rep.SuccessTerminals == 0 {
		t.Fatal("no successful terminal")
	}
	if rep.MinLatency[0] != 2 || rep.MaxLatency[0] != 4 {
		t.Errorf("p0 latency bounds = %d..%d, want 2..4", rep.MinLatency[0], rep.MaxLatency[0])
	}
}

// With 3 packets the ring can deadlock under some schedules; enumeration
// finds the stuck states and the evidence shows the circular wait.
func TestEnumerateRingFindsDeadlock(t *testing.T) {
	sim := ringSim(t, 3)
	rep := sim.Enumerate(100000)
	if rep.Overflow {
		t.Fatal("state space overflow")
	}
	if len(rep.InvariantErrors) > 0 {
		t.Fatalf("invariant violations: %v", rep.InvariantErrors)
	}
	if rep.DeadlockTerminals == 0 {
		t.Fatal("no deadlock found")
	}
	if rep.SuccessTerminals == 0 {
		t.Fatal("expected some schedules to succeed")
	}
	ev := sim.DeadlockEvidence(rep.DeadlockExample)
	if !strings.Contains(ev, "cycle:") {
		t.Errorf("deadlock example lacks a wait-for cycle:\n%s", ev)
	}
}

// The deterministic greedy run stays within the latency bounds proven
// by exhaustive enumeration.
func TestGreedyWithinEnumeratedBounds(t *testing.T) {
	sim := ringSim(t, 2)
	rep := sim.Enumerate(100000)
	res := sim.Run(sim.Initial(), 1000, nil)
	if !res.Done {
		t.Fatal("greedy run did not complete")
	}
	for id := range rep.MinLatency {
		lat, _ := sim.Latency(res.Final, id)
		if lat < rep.MinLatency[id] || lat > rep.MaxLatency[id] {
			t.Errorf("p%d greedy latency %d outside [%d, %d]",
				id, lat, rep.MinLatency[id], rep.MaxLatency[id])
		}
	}
}

func TestRoutingLoopRejected(t *testing.T) {
	net := NewNetwork(2)
	mustAddEdge(t, net, 0, 1, 1)
	mustAddEdge(t, net, 1, 0, 1)
	mustRoute(t, net, 0, 2, 1) // dst 2 does not even exist as a node pair
	mustRoute(t, net, 1, 2, 0)
	_, err := NewSim(net, []*Packet{{ID: 0, Src: 0, Dst: 2}})
	if err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("expected routing loop error, got %v", err)
	}
}

func TestMissingRouteRejected(t *testing.T) {
	net := NewNetwork(2)
	mustAddEdge(t, net, 0, 1, 1)
	if _, err := NewSim(net, []*Packet{{ID: 0, Src: 0, Dst: 1}}); err == nil {
		t.Fatal("expected missing route error")
	}
}
