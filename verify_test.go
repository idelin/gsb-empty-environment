package main

import (
	"testing"
)

func verifyInput() *Input {
	return &Input{
		Files: []FileInput{
			{ID: "base", Bytes: nat(10)},
			{ID: "inc", Bytes: nat(5), Dependencies: []string{"meta"}},
			{ID: "meta", Bytes: nat(0)},
			{ID: "bad", Bytes: nat(1), Corrupted: true},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 20, Edges: []EdgeInput{
				{From: 0, To: 10, File: "base"},
				{From: 10, To: 20, File: "inc"},
			}},
		},
	}
}

func TestVerifierAcceptsValidSolution(t *testing.T) {
	in := verifyInput()
	out := solve(in, -1)
	if out.Status != StatusOptimal {
		t.Fatalf("status = %s", out.Status)
	}
	v := verify(&VerifyRequest{Input: *in, Solution: *out.Solution})
	if !v.Valid {
		t.Fatalf("solver's own solution rejected: %s", v.Reason)
	}
	if v.ComputedTotalBytes != out.Solution.TotalBytes {
		t.Fatalf("computed %s != claimed %s", v.ComputedTotalBytes, out.Solution.TotalBytes)
	}
	if len(v.ShardPaths["s1"]) == 0 || v.ShardPaths["s1"][0] != 0 || v.ShardPaths["s1"][len(v.ShardPaths["s1"])-1] != 20 {
		t.Fatalf("bad witness path: %v", v.ShardPaths)
	}
}

func TestVerifierRejectsInvalidSolutions(t *testing.T) {
	in := verifyInput()
	cases := []struct {
		name string
		sol  SolutionJSON
	}{
		{"missing dependency", SolutionJSON{Files: []string{"base", "inc"}, TotalBytes: "15"}},
		{"wrong total", SolutionJSON{Files: []string{"base", "inc", "meta"}, TotalBytes: "14"}},
		{"corrupted file", SolutionJSON{Files: []string{"bad", "base", "inc", "meta"}, TotalBytes: "16"}},
		{"unknown id", SolutionJSON{Files: []string{"base", "inc", "meta", "ghost"}, TotalBytes: "15"}},
		{"incomplete restore", SolutionJSON{Files: []string{"base"}, TotalBytes: "10"}},
	}
	for _, tc := range cases {
		v := verify(&VerifyRequest{Input: *in, Solution: tc.sol})
		if v.Valid {
			t.Fatalf("%s: expected invalid, got valid", tc.name)
		}
		if v.Reason == "" {
			t.Fatalf("%s: missing rejection reason", tc.name)
		}
	}
}

// TestVerifierOvershootPathRejected: a solution relying on an edge that
// overshoots the target cannot be valid; the only legal restore here is the
// direct edge, and the verifier must confirm exactly that.
func TestVerifierOvershootPathRejected(t *testing.T) {
	in := &Input{
		Files: []FileInput{
			{ID: "over", Bytes: nat(1)},
			{ID: "back", Bytes: nat(1)},
			{ID: "direct", Bytes: nat(9)},
		},
		Shards: []ShardInput{
			{Name: "s1", Target: 10, Edges: []EdgeInput{
				{From: 0, To: 30, File: "over"},  // overshoots target
				{From: 30, To: 10, File: "back"}, // decreasing, invalid edge
				{From: 0, To: 10, File: "direct"},
			}},
		},
	}
	// The 30 -> 10 edge is not strictly increasing: hard input error.
	out := solve(in, -1)
	if out.Status != StatusError || out.Errors[0].Kind != "invalid_edge" {
		t.Fatalf("expected invalid_edge, got %+v", out)
	}

	// Without the decreasing edge, the overshooting edge is simply unusable.
	in.Shards[0].Edges = in.Shards[0].Edges[:1]
	in.Shards[0].Edges = append(in.Shards[0].Edges, EdgeInput{From: 0, To: 10, File: "direct"})
	out = solve(in, -1)
	if out.Status != StatusOptimal || out.Solution.TotalBytes != "9" {
		t.Fatalf("overshoot edge must be ignored: %s %+v", out.Status, out.Solution)
	}
	v := verify(&VerifyRequest{Input: *in, Solution: SolutionJSON{Files: []string{"over"}, TotalBytes: "1"}})
	if v.Valid {
		t.Fatal("solution using only an overshooting edge must be rejected")
	}
}
