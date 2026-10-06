package trail

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestSolveEmpty(t *testing.T) {
	route, err := Solve("A", nil)
	if err != nil || route != nil {
		t.Fatalf("got %v, %v; want nil, nil", route, err)
	}
}

func TestSolveCircuitLexOrder(t *testing.T) {
	// Parallel edges force alternation; the lexicographically smallest
	// alternation wins.
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "A", To: "B"},
		{ID: "e4", From: "B", To: "A"},
	}
	route, err := Solve("A", edges)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e1", "e2", "e3", "e4"}; !reflect.DeepEqual(route, want) {
		t.Fatalf("got %v, want %v", route, want)
	}
}

func TestSolveAvoidsGreedyTrap(t *testing.T) {
	// Naive smallest-ID-first would take e2 (B->A) at B and strand e3,e4.
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "B", To: "C"},
		{ID: "e4", From: "C", To: "B"},
	}
	route, err := Solve("A", edges)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e1", "e3", "e4", "e2"}; !reflect.DeepEqual(route, want) {
		t.Fatalf("got %v, want %v", route, want)
	}
}

func TestSolveOpenTrail(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "C"},
		{ID: "e3", From: "C", To: "B"},
	}
	route, err := Solve("A", edges)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e1", "e2", "e3"}; !reflect.DeepEqual(route, want) {
		t.Fatalf("got %v, want %v", route, want)
	}
}

func TestSolveSelfLoopFirst(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "A"},
		{ID: "e2", From: "A", To: "B"},
		{ID: "e3", From: "B", To: "A"},
	}
	route, err := Solve("A", edges)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"e1", "e2", "e3"}; !reflect.DeepEqual(route, want) {
		t.Fatalf("got %v, want %v", route, want)
	}
}

func TestSolveDegreeFailure(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "A", To: "B"},
	}
	_, err := Solve("A", edges)
	f, ok := err.(*Failure)
	if !ok {
		t.Fatalf("got %v, want *Failure", err)
	}
	if f.Kind != FailureDegree {
		t.Fatalf("got kind %v, want FailureDegree", f.Kind)
	}
	if len(f.Nodes) == 0 {
		t.Fatal("expected imbalanced nodes to be reported")
	}
}

func TestSolveWrongStartDegreeFailure(t *testing.T) {
	// A valid open trail exists from B, but not from A.
	edges := []Edge{{ID: "e1", From: "B", To: "A"}}
	_, err := Solve("A", edges)
	f, ok := err.(*Failure)
	if !ok || f.Kind != FailureDegree {
		t.Fatalf("got %v, want degree failure", err)
	}
}

func TestSolveConnectivityFailure(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "C", To: "D"},
		{ID: "e4", From: "D", To: "C"},
	}
	_, err := Solve("A", edges)
	f, ok := err.(*Failure)
	if !ok {
		t.Fatalf("got %v, want *Failure", err)
	}
	if f.Kind != FailureConnectivity {
		t.Fatalf("got kind %v, want FailureConnectivity", f.Kind)
	}
	if want := []string{"e3", "e4"}; !reflect.DeepEqual(f.EdgeIDs, want) {
		t.Fatalf("got unreachable %v, want %v", f.EdgeIDs, want)
	}
}

func TestSolveDuplicateID(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e1", From: "B", To: "A"},
	}
	if _, err := Solve("A", edges); err == nil {
		t.Fatal("expected duplicate ID error")
	} else if _, ok := err.(*DuplicateEdgeIDError); !ok {
		t.Fatalf("got %T, want *DuplicateEdgeIDError", err)
	}
}

// TestSolveMatchesExhaustive fuzzes small instances: whenever Solve finds
// a route it must be a valid trail and equal to the exhaustive optimum.
func TestSolveMatchesExhaustive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	nodes := []string{"A", "B", "C", "D"}
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(8)
		edges := make([]Edge, 0, n)
		for i := 0; i < n; i++ {
			edges = append(edges, Edge{
				ID:   string(rune('a' + i)),
				From: nodes[rng.Intn(len(nodes))],
				To:   nodes[rng.Intn(len(nodes))],
			})
		}
		start := nodes[rng.Intn(len(nodes))]
		route, err := Solve(start, edges)
		if err != nil {
			continue
		}
		assertValidTrail(t, start, edges, route)
		opt, ok := exhaustiveRoute(start, edges, nil)
		if !ok {
			t.Fatalf("trial %d: Solve found %v but exhaustive search failed", trial, route)
		}
		if !reflect.DeepEqual(route, opt) {
			t.Fatalf("trial %d: greedy %v != exhaustive %v", trial, route, opt)
		}
	}
}

func assertValidTrail(t *testing.T, start string, edges []Edge, route []string) {
	t.Helper()
	if len(route) != len(edges) {
		t.Fatalf("route length %d, want %d", len(route), len(edges))
	}
	byID := map[string]Edge{}
	for _, e := range edges {
		byID[e.ID] = e
	}
	seen := map[string]bool{}
	cur := start
	for _, id := range route {
		e, ok := byID[id]
		if !ok {
			t.Fatalf("route uses unknown edge %q", id)
		}
		if seen[id] {
			t.Fatalf("route reuses edge %q", id)
		}
		seen[id] = true
		if e.From != cur {
			t.Fatalf("edge %q departs %q, expected %q", id, e.From, cur)
		}
		cur = e.To
	}
}
