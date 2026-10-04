package main

import (
	"math"
	"math/rand"
	"testing"
)

// Cross-check the exact subset Dijkstra against brute-force subset
// enumeration on many small random hypergraphs, and validate proofs.
func TestRandomAgainstEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		nNodes := 3 + rng.Intn(3) // 3..5 nodes
		names := make([]string, nNodes)
		for i := range names {
			names[i] = "n" + itoa(i)
		}
		p := &Problem{}
		nFacts := rng.Intn(3) // 0..2 facts
		for i := 0; i < nFacts; i++ {
			p.Facts = append(p.Facts, Fact{Name: names[i], Cost: float64(rng.Intn(3))})
		}
		nEdges := 2 + rng.Intn(7) // 2..8 edges
		for i := 0; i < nEdges; i++ {
			nPre := rng.Intn(3) // 0..2 premises
			perm := rng.Perm(nNodes)
			pre := make([]string, nPre)
			for j := range pre {
				pre[j] = names[perm[j]]
			}
			p.Edges = append(p.Edges, Hyperedge{
				ID:       "e" + itoa(i),
				Premises: pre,
				Target:   names[rng.Intn(nNodes)],
				Cost:     float64(rng.Intn(5)),
			})
		}

		res := p.Solve(0)
		aug := p.augmented()
		live, _ := liveEdges(aug)
		for _, node := range names {
			nr := res.Nodes[node]
			enumCost, _, ok := enumMinCost(live, node)
			if !ok {
				t.Fatal("enumeration infeasible for small test")
			}
			derivable := !math.IsInf(enumCost, 1)
			if nr == nil {
				// Node is not the target of any edge (nor a fact).
				if derivable {
					t.Fatalf("trial %d node %s: unproducible node enumerated", trial, node)
				}
				continue
			}
			if nr.Derivable != derivable {
				t.Fatalf("trial %d node %s: derivable=%v, enumeration=%v",
					trial, node, nr.Derivable, derivable)
			}
			if !derivable {
				continue
			}
			if !nr.Exact {
				t.Fatalf("trial %d node %s: not exact without truncation", trial, node)
			}
			if math.Abs(nr.Cost-enumCost) > eps {
				t.Fatalf("trial %d node %s: exact=%v enumeration=%v",
					trial, node, nr.Cost, enumCost)
			}
			if nr.Lower > nr.Cost+eps || nr.Upper < nr.Cost-eps {
				t.Fatalf("trial %d node %s: bounds [%v,%v] miss %v",
					trial, node, nr.Lower, nr.Upper, nr.Cost)
			}
			checkProof(t, res, nr)
		}
	}
}

// checkProof verifies that the returned proof really derives the node and
// that its cost matches the recorded optimum.
func checkProof(t *testing.T, res *SolveResult, nr *NodeResult) {
	var edges []Hyperedge
	cost := 0.0
	seen := map[int]bool{}
	for _, ei := range nr.Proof {
		if seen[ei] {
			t.Fatalf("node %s: proof reuses edge %d", nr.Node, ei)
		}
		seen[ei] = true
		edges = append(edges, res.Edges[ei])
		cost += res.Edges[ei].Cost
	}
	if math.Abs(cost-nr.Cost) > eps {
		t.Fatalf("node %s: proof cost %v != recorded %v", nr.Node, cost, nr.Cost)
	}
	if !closure(edges)[nr.Node] {
		t.Fatalf("node %s: proof edges do not derive it", nr.Node)
	}
	// Firing order must be valid: every premise proven by an earlier step.
	available := map[string]bool{}
	for _, ei := range nr.Proof {
		e := res.Edges[ei]
		for _, pre := range e.Premises {
			if !available[pre] {
				t.Fatalf("node %s: premise %s not ready when edge %s fires",
					nr.Node, pre, e.ID)
			}
		}
		available[e.Target] = true
	}
}
