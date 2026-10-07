package traj

import (
	"math/rand"
	"reflect"
	"testing"
)

func ids(route []Edge) []string { return RouteIDs(route) }

func TestFindRouteLexicographic(t *testing.T) {
	// Naive smallest-first greedy would take e1 (A->B) and get stuck;
	// the only valid trail is e2, e3, e1.
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "A", To: "C"},
		{ID: "e3", From: "C", To: "A"},
	}
	route, diag := FindRoute(edges, "A")
	if diag != nil {
		t.Fatalf("unexpected diagnosis: %v", diag)
	}
	if want := []string{"e2", "e3", "e1"}; !reflect.DeepEqual(ids(route), want) {
		t.Fatalf("got %v, want %v", ids(route), want)
	}
}

func TestFindRouteDuplicateLegs(t *testing.T) {
	// Two parallel edges A->B with distinct IDs; the smaller ID must
	// be taken first.
	edges := []Edge{
		{ID: "x2", From: "A", To: "B"},
		{ID: "x1", From: "A", To: "B"},
		{ID: "x3", From: "B", To: "A"},
	}
	route, diag := FindRoute(edges, "A")
	if diag != nil {
		t.Fatalf("unexpected diagnosis: %v", diag)
	}
	if want := []string{"x1", "x3", "x2"}; !reflect.DeepEqual(ids(route), want) {
		t.Fatalf("got %v, want %v", ids(route), want)
	}
}

func TestFindRouteDegreeFailure(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "A", To: "C"},
	}
	_, diag := FindRoute(edges, "A")
	if diag == nil || diag.Kind != FailDegree {
		t.Fatalf("want degree failure, got %v", diag)
	}
	if diag.Balance["A"] != 2 {
		t.Fatalf("balance of A = %d, want 2", diag.Balance["A"])
	}
}

func TestFindRouteWrongStartDegree(t *testing.T) {
	// Valid trail exists but must start at B, not A.
	edges := []Edge{
		{ID: "e1", From: "B", To: "A"},
		{ID: "e2", From: "A", To: "C"},
	}
	_, diag := FindRoute(edges, "A")
	if diag == nil || diag.Kind != FailDegree {
		t.Fatalf("want degree failure, got %v", diag)
	}
}

func TestFindRouteConnectivityFailure(t *testing.T) {
	edges := []Edge{
		{ID: "e1", From: "A", To: "B"},
		{ID: "e2", From: "B", To: "A"},
		{ID: "e3", From: "C", To: "D"},
		{ID: "e4", From: "D", To: "C"},
	}
	_, diag := FindRoute(edges, "A")
	if diag == nil || diag.Kind != FailConnectivity {
		t.Fatalf("want connectivity failure, got %v", diag)
	}
	if !reflect.DeepEqual(diag.Unreachable, []string{"C", "D"}) {
		t.Fatalf("unreachable = %v", diag.Unreachable)
	}
}

func TestFindRouteEmpty(t *testing.T) {
	route, diag := FindRoute(nil, "A")
	if diag != nil || len(route) != 0 {
		t.Fatalf("empty edge set: route=%v diag=%v", route, diag)
	}
}

// TestFindRouteAgainstBruteForce randomises small multigraphs and
// cross-checks FindRoute against exhaustive permutation enumeration.
func TestFindRouteAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	nodes := []string{"A", "B", "C", "D"}
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(8) // 1..8 edges
		edges := make([]Edge, n)
		for i := range edges {
			edges[i] = Edge{
				ID:   string(rune('a' + i)),
				From: nodes[rng.Intn(len(nodes))],
				To:   nodes[rng.Intn(len(nodes))],
			}
		}
		start := nodes[rng.Intn(len(nodes))]
		route, diag := FindRoute(edges, start)
		ok, err := VerifyExhaustive(edges, start, route)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if !ok {
			best, found := BruteForceMinRoute(edges, start)
			t.Fatalf("trial %d: edges=%v start=%s route=%v diag=%v brute=%v found=%v",
				trial, edges, start, ids(route), diag, best, found)
		}
		if diag == nil && len(route) != n {
			t.Fatalf("trial %d: route length %d, want %d", trial, len(route), n)
		}
	}
}
