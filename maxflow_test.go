package maxflow

import (
	"reflect"
	"testing"
)

// Node ids for the acceptance scenario.
//
//	s=0, a=1, b=2, c=3, d=4, t=5
//
//	s->a 5, s->b 5
//	a->c 5 (key edge), b->c 5, c->t 10
//	a->d 1, d->t 1 (alternative path, capacity 1)
const (
	nodeS = 0
	nodeA = 1
	nodeB = 2
	nodeC = 3
	nodeD = 4
	nodeT = 5
)

func buildAcceptanceNetwork(t *testing.T) *Network {
	t.Helper()
	n := NewNetwork(6, nodeS, nodeT)
	edges := []struct {
		u, v int
		cap  int64
	}{
		{nodeS, nodeA, 5},
		{nodeS, nodeB, 5},
		{nodeA, nodeC, 5}, // key edge
		{nodeB, nodeC, 5},
		{nodeC, nodeT, 10},
		{nodeA, nodeD, 1},
		{nodeD, nodeT, 1},
	}
	for _, e := range edges {
		if err := n.AddEdge(e.u, e.v, e.cap); err != nil {
			t.Fatalf("AddEdge(%d,%d,%d): %v", e.u, e.v, e.cap, err)
		}
	}
	return n
}

// maxFlowFromScratch rebuilds a network with the same capacities and computes
// the max flow without any incremental state, for cross-checking.
func maxFlowFromScratch(t *testing.T, n *Network) int64 {
	t.Helper()
	fresh := NewNetwork(n.n, n.s, n.t)
	for _, key := range n.order {
		e := n.fwd[key]
		if err := fresh.AddEdge(key[0], key[1], e.Cap); err != nil {
			t.Fatalf("rebuild AddEdge: %v", err)
		}
	}
	return fresh.MaxFlow()
}

func assertCutConsistent(t *testing.T, n *Network, cert Certificate) {
	t.Helper()
	var cutCap int64
	for _, ce := range cert.CutEdges {
		cutCap += ce.Cap
		if ce.Flow != ce.Cap {
			t.Errorf("cut edge %d->%d not saturated: flow %d cap %d", ce.From, ce.To, ce.Flow, ce.Cap)
		}
	}
	if cutCap != cert.Value {
		t.Errorf("cut capacity %d != flow value %d", cutCap, cert.Value)
	}
	inS := make(map[int]bool)
	for _, x := range cert.SSide {
		inS[x] = true
	}
	if !inS[n.s] {
		t.Errorf("source not on S side: %v", cert.SSide)
	}
	if inS[n.t] {
		t.Errorf("sink not on T side: %v", cert.TSide)
	}
	if len(cert.SSide)+len(cert.TSide) != n.n {
		t.Errorf("partition does not cover all nodes: S=%v T=%v", cert.SSide, cert.TSide)
	}
	if got := maxFlowFromScratch(t, n); got != cert.Value {
		t.Errorf("incremental value %d disagrees with from-scratch max flow %d", cert.Value, got)
	}
}

func TestInitialMaxFlow(t *testing.T) {
	n := buildAcceptanceNetwork(t)
	if got := n.MaxFlow(); got != 10 {
		t.Fatalf("initial max flow = %d, want 10", got)
	}
	if f, _ := n.Flow(nodeA, nodeC); f != 5 {
		t.Fatalf("key edge a->c flow = %d, want 5", f)
	}
	cert := n.Cert()
	if cert.Value != 10 {
		t.Fatalf("certificate value = %d, want 10", cert.Value)
	}
	assertCutConsistent(t, n, cert)
}

// Acceptance boundary: key edge a->c drops 5 -> 2; 3 units are withdrawn
// along reverse augmenting tracks, 1 unit is recovered via the alternative
// path s->a->d->t, final flow 8, with cut edges and S/T partition reported.
func TestCapacityDecreaseWithdrawal(t *testing.T) {
	n := buildAcceptanceNetwork(t)
	if got := n.MaxFlow(); got != 10 {
		t.Fatalf("initial max flow = %d, want 10", got)
	}

	cert, err := n.Adjust([]Change{{From: nodeA, To: nodeC, Delta: -3}})
	if err != nil {
		t.Fatalf("Adjust: %v", err)
	}

	if cert.Value != 8 {
		t.Errorf("adjusted max flow = %d, want 8", cert.Value)
	}
	if f, _ := n.Flow(nodeA, nodeC); f != 2 {
		t.Errorf("key edge a->c flow = %d, want 2 (3 units withdrawn)", f)
	}
	// 3 units withdrawn from s->a, 1 unit re-routed via a->d->t.
	if f, _ := n.Flow(nodeS, nodeA); f != 3 {
		t.Errorf("s->a flow = %d, want 3", f)
	}
	if f, _ := n.Flow(nodeA, nodeD); f != 1 {
		t.Errorf("alternative path a->d flow = %d, want 1", f)
	}

	wantS := []int{nodeS, nodeA}
	if !reflect.DeepEqual(cert.SSide, wantS) {
		t.Errorf("S side = %v, want %v", cert.SSide, wantS)
	}
	wantT := []int{nodeB, nodeC, nodeD, nodeT}
	if !reflect.DeepEqual(cert.TSide, wantT) {
		t.Errorf("T side = %v, want %v", cert.TSide, wantT)
	}
	wantCut := []CutEdge{
		{From: nodeS, To: nodeB, Cap: 5, Flow: 5},
		{From: nodeA, To: nodeC, Cap: 2, Flow: 2},
		{From: nodeA, To: nodeD, Cap: 1, Flow: 1},
	}
	if !reflect.DeepEqual(cert.CutEdges, wantCut) {
		t.Errorf("cut edges = %+v, want %+v", cert.CutEdges, wantCut)
	}
	assertCutConsistent(t, n, cert)
}

func TestCapacityIncrease(t *testing.T) {
	n := buildAcceptanceNetwork(t)
	n.MaxFlow()
	if _, err := n.Adjust([]Change{{From: nodeA, To: nodeC, Delta: -3}}); err != nil {
		t.Fatalf("Adjust decrease: %v", err)
	}
	if got := n.Value(); got != 8 {
		t.Fatalf("value after decrease = %d, want 8", got)
	}
	cert, err := n.Adjust([]Change{{From: nodeA, To: nodeC, Delta: 3}})
	if err != nil {
		t.Fatalf("Adjust increase: %v", err)
	}
	if cert.Value != 10 {
		t.Errorf("value after restoring capacity = %d, want 10", cert.Value)
	}
	assertCutConsistent(t, n, cert)
}

// Batch change: shrink the key edge and widen the alternative path at once.
func TestBatchChanges(t *testing.T) {
	n := buildAcceptanceNetwork(t)
	n.MaxFlow()
	cert, err := n.Adjust([]Change{
		{From: nodeA, To: nodeC, Delta: -3}, // key edge 5 -> 2
		{From: nodeA, To: nodeD, Delta: 2},  // alternative 1 -> 3
		{From: nodeD, To: nodeT, Delta: 2},  // alternative 1 -> 3
	})
	if err != nil {
		t.Fatalf("Adjust batch: %v", err)
	}
	if cert.Value != 10 {
		t.Errorf("batch-adjusted value = %d, want 10", cert.Value)
	}
	assertCutConsistent(t, n, cert)
}

func TestAdjustValidation(t *testing.T) {
	n := buildAcceptanceNetwork(t)
	n.MaxFlow()

	if _, err := n.Adjust([]Change{{From: nodeA, To: nodeT, Delta: 1}}); err == nil {
		t.Error("expected error for non-existent edge")
	}
	if _, err := n.Adjust([]Change{{From: nodeA, To: nodeC, Delta: -6}}); err == nil {
		t.Error("expected error for negative resulting capacity")
	}
	// Failed batch must not mutate capacities.
	if c, _ := n.Capacity(nodeA, nodeC); c != 5 {
		t.Errorf("capacity mutated by rejected batch: got %d, want 5", c)
	}
	if err := n.AddEdge(nodeS, nodeA, 1); err == nil {
		t.Error("expected error for duplicate edge")
	}
	if err := n.AddEdge(nodeS, nodeB, -1); err == nil {
		t.Error("expected error for negative capacity")
	}
}

// Randomized cross-check: random batch adjustments must always agree with a
// from-scratch max-flow computation and yield a valid cut.
func TestRandomizedConsistency(t *testing.T) {
	seed := int64(42)
	rand := func(mod int64) int64 { // deterministic LCG, no external deps
		seed = seed*6364136223846793005 + 1442695040888963407
		return (seed >> 33) % mod
	}
	for trial := 0; trial < 50; trial++ {
		n := NewNetwork(6, nodeS, nodeT)
		caps := [][3]int64{
			{nodeS, nodeA, 5}, {nodeS, nodeB, 5},
			{nodeA, nodeC, 5}, {nodeB, nodeC, 5},
			{nodeC, nodeT, 10}, {nodeA, nodeD, 3}, {nodeD, nodeT, 3},
		}
		for _, e := range caps {
			if err := n.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
				t.Fatal(err)
			}
		}
		n.MaxFlow()
		for step := 0; step < 20; step++ {
			var batch []Change
			for _, e := range caps {
				if rand(2) == 0 {
					continue
				}
				delta := rand(5) - rand(5)
				batch = append(batch, Change{From: int(e[0]), To: int(e[1]), Delta: delta})
			}
			// Keep capacities non-negative.
			valid := true
			for _, c := range batch {
				cur, _ := n.Capacity(c.From, c.To)
				if cur+c.Delta < 0 {
					valid = false
				}
			}
			if !valid || len(batch) == 0 {
				continue
			}
			cert, err := n.Adjust(batch)
			if err != nil {
				t.Fatalf("trial %d step %d: Adjust: %v", trial, step, err)
			}
			assertCutConsistent(t, n, cert)
		}
	}
}
