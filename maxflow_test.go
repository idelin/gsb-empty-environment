package maxflow

import (
	"reflect"
	"testing"
)

// Node layout shared by the acceptance scenario:
//
//	s(0) --5--> a(1) --5--> t(3)
//	  \          |           ^
//	   5         1           |
//	    \        v           6
//	     \-----> b(2) -------/
//
// Initial max flow: 10 (s->a->t carries 5, s->b->t carries 5).
// Reducing a->t from 5 to 2 forces 3 units back along the residual
// network; 1 unit is recovered via s->a->b->t, giving max flow 8.
const (
	nodeS = 0
	nodeA = 1
	nodeB = 2
	nodeT = 3
)

type acceptanceNet struct {
	net                     *Network
	eSA, eSB, eAT, eAB, eBT int
}

func buildAcceptanceNet() acceptanceNet {
	n := NewNetwork(4)
	an := acceptanceNet{net: n}
	an.eSA = n.AddEdge(nodeS, nodeA, 5)
	an.eSB = n.AddEdge(nodeS, nodeB, 5)
	an.eAT = n.AddEdge(nodeA, nodeT, 5) // critical edge
	an.eAB = n.AddEdge(nodeA, nodeB, 1)
	an.eBT = n.AddEdge(nodeB, nodeT, 6)
	return an
}

func TestInitialMaxFlow(t *testing.T) {
	an := buildAcceptanceNet()
	if got := an.net.MaxFlow(nodeS, nodeT); got != 10 {
		t.Fatalf("initial max flow = %d, want 10", got)
	}
}

// Acceptance boundary: critical edge a->t drops 5 -> 2; 3 units are
// retracted, 1 unit re-augmented via the alternative path, final flow 8,
// with the cut certificate {s->b, a->t, a->b} and partition {s,a}|{b,t}.
func TestCapacityReductionWithRetraction(t *testing.T) {
	an := buildAcceptanceNet()
	an.net.MaxFlow(nodeS, nodeT)

	res := an.net.UpdateCapacity(an.eAT, 2)

	if res.MaxFlow != 8 {
		t.Errorf("max flow after reduction = %d, want 8", res.MaxFlow)
	}
	if res.Retracted != 3 {
		t.Errorf("retracted flow = %d, want 3", res.Retracted)
	}
	if res.ReAugmented != 1 {
		t.Errorf("re-augmented flow = %d, want 1", res.ReAugmented)
	}
	wantReach := []bool{true, true, false, false} // S side: {s, a}
	if !reflect.DeepEqual(res.Reachable, wantReach) {
		t.Errorf("reachability partition = %v, want %v", res.Reachable, wantReach)
	}
	wantCut := []CutEdge{
		{ID: an.eSB, From: nodeS, To: nodeB, Cap: 5},
		{ID: an.eAT, From: nodeA, To: nodeT, Cap: 2},
		{ID: an.eAB, From: nodeA, To: nodeB, Cap: 1},
	}
	if !reflect.DeepEqual(res.Cut, wantCut) {
		t.Errorf("cut edges = %+v, want %+v", res.Cut, wantCut)
	}
	var cutCap int64
	for _, ce := range res.Cut {
		cutCap += ce.Cap
	}
	if cutCap != res.MaxFlow {
		t.Errorf("cut capacity %d != max flow %d", cutCap, res.MaxFlow)
	}
	// The alternative path s->a->b->t must now carry the recovered unit.
	if got := an.net.Flow(an.eAB); got != 1 {
		t.Errorf("flow on a->b = %d, want 1", got)
	}
	if got := an.net.Flow(an.eAT); got != 2 {
		t.Errorf("flow on a->t = %d, want 2", got)
	}
}

func TestCapacityIncrease(t *testing.T) {
	an := buildAcceptanceNet()
	an.net.MaxFlow(nodeS, nodeT)
	an.net.UpdateCapacity(an.eAT, 2) // drop to 8 first

	res := an.net.UpdateCapacity(an.eAT, 5) // restore
	if res.MaxFlow != 10 {
		t.Errorf("max flow after increase = %d, want 10", res.MaxFlow)
	}
	if res.Retracted != 0 {
		t.Errorf("retracted = %d, want 0 on capacity increase", res.Retracted)
	}
	if res.ReAugmented != 2 {
		t.Errorf("re-augmented = %d, want 2", res.ReAugmented)
	}
}

func TestBatchUpdate(t *testing.T) {
	an := buildAcceptanceNet()
	an.net.MaxFlow(nodeS, nodeT)

	// Reduce a->t (5->2, retracts 3) and b->t (6->5, no violation) at once.
	res := an.net.BatchUpdate([]CapUpdate{
		{ID: an.eAT, NewCap: 2},
		{ID: an.eBT, NewCap: 5},
	})
	if res.MaxFlow != 7 {
		t.Errorf("max flow after batch = %d, want 7", res.MaxFlow)
	}
	if res.Retracted != 3 {
		t.Errorf("retracted = %d, want 3", res.Retracted)
	}
	if res.ReAugmented != 0 {
		t.Errorf("re-augmented = %d, want 0", res.ReAugmented)
	}
	// Cut {s,a,b}|{t}: edges a->t (2) and b->t (5).
	wantCut := []CutEdge{
		{ID: an.eAT, From: nodeA, To: nodeT, Cap: 2},
		{ID: an.eBT, From: nodeB, To: nodeT, Cap: 5},
	}
	if !reflect.DeepEqual(res.Cut, wantCut) {
		t.Errorf("cut edges = %+v, want %+v", res.Cut, wantCut)
	}
}

func TestReductionWithoutViolationKeepsFlow(t *testing.T) {
	an := buildAcceptanceNet()
	an.net.MaxFlow(nodeS, nodeT)

	// a->b carries no flow initially; shrinking it changes nothing.
	res := an.net.UpdateCapacity(an.eAB, 0)
	if res.MaxFlow != 10 {
		t.Errorf("max flow = %d, want 10", res.MaxFlow)
	}
	if res.Retracted != 0 || res.ReAugmented != 0 {
		t.Errorf("retracted=%d re-augmented=%d, want 0/0", res.Retracted, res.ReAugmented)
	}
}

func TestCertificateMatchesBruteForce(t *testing.T) {
	// After every update, the certified cut capacity must equal the flow
	// value, and the flow must be a valid max flow (no augmenting path).
	an := buildAcceptanceNet()
	n := an.net
	n.MaxFlow(nodeS, nodeT)
	seq := []CapUpdate{
		{ID: an.eAT, NewCap: 2},
		{ID: an.eAT, NewCap: 5},
		{ID: an.eBT, NewCap: 3},
		{ID: an.eSA, NewCap: 1},
		{ID: an.eSB, NewCap: 9},
	}
	for _, u := range seq {
		res := n.UpdateCapacity(u.ID, u.NewCap)
		var cutCap int64
		for _, ce := range res.Cut {
			cutCap += ce.Cap
		}
		if cutCap != res.MaxFlow {
			t.Fatalf("after %+v: cut capacity %d != max flow %d", u, cutCap, res.MaxFlow)
		}
		if !res.Reachable[nodeS] || res.Reachable[nodeT] {
			t.Fatalf("after %+v: invalid partition %v", u, res.Reachable)
		}
	}
	// Final network: s->a:1, s->b:9, a->t:5, a->b:1, b->t:3.
	// Max flow = min(s-out=10, t-in=8, ...) = 1+3 = 4 via s->a->b->t etc.
	if got := n.Value(); got != 4 {
		t.Fatalf("final max flow = %d, want 4", got)
	}
}
