package netsec

import "testing"

// Two chains share a login prefix event; the assignment must not double-count
// it and must still find the maximum number of complete disjoint chains.
func TestSharedPrefix(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "e1", Stage: "login", Time: 0},
			{ID: "e2", Stage: "exploit", Time: 10},
			{ID: "e3", Stage: "exfil", Time: 20},
			{ID: "e4", Stage: "exploit", Time: 30},
			{ID: "e5", Stage: "exfil", Time: 40},
		},
		Templates: []ChainTemplate{
			{Name: "intrusion", Stages: []string{"login", "exploit", "exfil"}},
		},
		MaxGap: 100,
	}
	got := assertMatchesOracle(t, req)
	if len(got.Chains) != 1 {
		t.Fatalf("expected one chain (shared login cannot be reused), got %d",
			len(got.Chains))
	}
	ids := got.Chains[0].EventIDs
	if len(ids) != 3 || ids[0] != "e1" {
		t.Fatalf("unexpected chain events: %v", ids)
	}
}

// Two templates fight over the same middle event; the search must explore both
// full-universe sides and pick the allocation with more complete chains.
func TestMiddleContention(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "a", Stage: "login", Time: 0},
			{ID: "b", Stage: "pivot", Time: 5},
			{ID: "c", Stage: "login", Time: 6},
			{ID: "d", Stage: "pivot", Time: 10},
			{ID: "e", Stage: "drop", Time: 12},
		},
		Templates: []ChainTemplate{
			{Name: "lateral", Stages: []string{"login", "pivot", "drop"}},
			{Name: "recon", Stages: []string{"login", "pivot"}},
		},
		MaxGap: 100,
	}
	got := assertMatchesOracle(t, req)
	if len(got.Chains) != 2 {
		t.Fatalf("expected two disjoint chains, got %d: %+v",
			len(got.Chains), got.Chains)
	}
}

func TestGapBoundary(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "x1", Stage: "s0", Time: 0},
			{ID: "x2", Stage: "s1", Time: 5},
			{ID: "x3", Stage: "s2", Time: 10},
		},
		Templates: []ChainTemplate{
			{Name: "t", Stages: []string{"s0", "s1", "s2"}},
		},
		MaxGap: 5,
	}
	assertMatchesOracle(t, req)

	tooTight := req
	tooTight.MaxGap = 4
	got, err := Assign(tooTight)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 0 {
		t.Fatalf("expected no chain beyond MaxGap, got %+v", got.Chains)
	}
}

func TestMaxSpan(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "m1", Stage: "s0", Time: 0},
			{ID: "m2", Stage: "s1", Time: 4},
			{ID: "m3", Stage: "s2", Time: 10},
		},
		Templates: []ChainTemplate{
			{Name: "t", Stages: []string{"s0", "s1", "s2"}},
		},
		MaxGap:  100,
		MaxSpan: 9,
	}
	got, err := Assign(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chains) != 0 {
		t.Fatalf("chain outside MaxSpan must not be confirmed, got %+v", got.Chains)
	}

	req.MaxSpan = 10
	assertMatchesOracle(t, req)
}

// Span tie-break: same chain count, the smaller-total-span allocation wins.
func TestSpanTieBreak(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "p1", Stage: "a", Time: 0},
			{ID: "p2", Stage: "b", Time: 1},
			{ID: "p3", Stage: "a", Time: 10},
			{ID: "p4", Stage: "b", Time: 11},
		},
		Templates: []ChainTemplate{
			{Name: "ab", Stages: []string{"a", "b"}},
		},
		MaxGap: 100,
	}
	got := assertMatchesOracle(t, req)
	if len(got.Chains) != 2 || got.TotalSpan != 2 {
		t.Fatalf("expected two chains of total span 2, got %+v span %d",
			got.Chains, got.TotalSpan)
	}
}

// When chain count and total span tie, the assignment whose ordered event-ID
// tuples are lexicographically smallest must win.
func TestEventIDTieBreak(t *testing.T) {
	req := Request{
		Events: []Event{
			{ID: "a", Stage: "x", Time: 0},
			{ID: "b", Stage: "y", Time: 1},
			{ID: "c", Stage: "x", Time: 2},
			{ID: "d", Stage: "y", Time: 3},
		},
		Templates: []ChainTemplate{
			{Name: "xy", Stages: []string{"x", "y"}},
		},
		MaxGap: 100,
	}
	got := assertMatchesOracle(t, req)
	// Two disjoint pairs exist with identical span; the pair starting at the
	// smallest event ID "a" must be selected first in the canonical result.
	if len(got.Chains) != 2 {
		t.Fatalf("expected two chains, got %d", len(got.Chains))
	}
	if got.Chains[0].EventIDs[0] != "a" {
		t.Fatalf("expected lexicographically smallest chain first, got %+v",
			got.Chains[0].EventIDs)
	}
}
