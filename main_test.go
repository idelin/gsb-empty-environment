package main

import (
	"reflect"
	"testing"
)

func diamondGraph() *Graph {
	return &Graph{Source: "s", Sink: "t", Edges: []Edge{
		{From: "s", To: "a", Capacity: 1},
		{From: "s", To: "b", Capacity: 1},
		{From: "a", To: "t", Capacity: 1},
		{From: "b", To: "t", Capacity: 1},
	}}
}

func TestPaginationWalk(t *testing.T) {
	g := diamondGraph()
	res := mustSolve(t, g, -1)

	var got [][]string
	cursor := ""
	for {
		page := buildPage(g, res, 1, cursor, 0)
		if page == nil {
			t.Fatal("buildPage failed")
		}
		if page.Verification.PerCutCapacity != "ok" {
			t.Fatalf("per-cut verification: %s", page.Verification.PerCutCapacity)
		}
		for _, c := range page.Cuts {
			got = append(got, c.Nodes)
			if c.Capacity != res.MaxFlow {
				t.Fatalf("cut %v capacity %d != max flow %d", c.Nodes, c.Capacity, res.MaxFlow)
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	want := [][]string{{"a", "b", "s"}, {"a", "s"}, {"b", "s"}, {"s"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paginated cuts = %v, want %v", got, want)
	}
}

func TestCursorBoundToGraphVersion(t *testing.T) {
	g := diamondGraph()
	res := mustSolve(t, g, -1)

	foreign := encodeCursor("some-other-version", 0)
	if page := buildPage(g, res, 2, foreign, 0); page != nil {
		t.Fatal("expected version-mismatched cursor to be rejected")
	}

	bogus := "not-a-cursor"
	if page := buildPage(g, res, 2, bogus, 0); page != nil {
		t.Fatal("expected malformed cursor to be rejected")
	}
}

func TestIncompleteEnumerationNeverClaimsComplete(t *testing.T) {
	g := &Graph{Source: "s", Sink: "t", Nodes: []string{"a", "b", "c", "d"},
		Edges: []Edge{{From: "s", To: "t", Capacity: 1}}}
	res := mustSolve(t, g, 3)
	page := buildPage(g, res, 2, "", 0)
	if page == nil {
		t.Fatal("buildPage failed")
	}
	if page.Complete {
		t.Fatal("budget-truncated enumeration must not claim completeness")
	}
	if page.KnownTotal != 3 {
		t.Fatalf("known total = %d, want 3", page.KnownTotal)
	}
	// Walking past the known cuts still must not claim completeness.
	last := buildPage(g, res, 2, page.NextCursor, 0)
	if last == nil {
		t.Fatal("buildPage failed")
	}
	if last.Complete || last.NextCursor != "" {
		t.Fatalf("final truncated page: complete=%v next=%q, want incomplete with no cursor", last.Complete, last.NextCursor)
	}
}

func TestBruteForceVerificationRuns(t *testing.T) {
	g := diamondGraph()
	res := mustSolve(t, g, -1)
	page := buildPage(g, res, 10, "", 20)
	if page == nil {
		t.Fatal("buildPage failed")
	}
	if page.Verification.BruteForce != "ok" {
		t.Fatalf("brute force check = %q, want ok", page.Verification.BruteForce)
	}
}
