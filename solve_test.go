package main

import (
	"math/big"
	"strings"
	"testing"
)

func mustParse(t *testing.T, js string) *Input {
	t.Helper()
	in, err := ParseInput([]byte(js))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	return in
}

func mustSolve(t *testing.T, js string) (*Input, *Solution) {
	t.Helper()
	in := mustParse(t, js)
	sol, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if probs := Verify(in, sol); len(probs) > 0 {
		t.Fatalf("self-verification failed: %v", probs)
	}
	return in, sol
}

// Exact rational CoG: 1/3 and 2/3 weights give CoG 2/3 (or 1/3), a fraction
// that has no finite binary/decimal form; the solver must report it exactly.
func TestExactRationalCoG(t *testing.T) {
	_, sol := mustSolve(t, `{
	  "capacity": "10",
	  "positions": ["0", "1"],
	  "safe_interval": ["0", "1"],
	  "items": [{"id":"a","weight":"1/3"}, {"id":"b","weight":"2/3"}]
	}`)
	if sol.BinsUsed != 1 {
		t.Fatalf("bins_used = %d, want 1", sol.BinsUsed)
	}
	cog := sol.Bins[0].Cog
	if cog != "1/3" && cog != "2/3" {
		t.Fatalf("cog = %q, want exact 1/3 or 2/3", cog)
	}
	if r, _ := new(big.Rat).SetString(cog); r == nil {
		t.Fatalf("cog %q not parseable as rational", cog)
	}
}

// Fewest bins: total weight 20 with capacity 10 needs exactly 2 bins.
func TestMinBins(t *testing.T) {
	_, sol := mustSolve(t, `{
	  "capacity": "10",
	  "positions": ["0", "10"],
	  "safe_interval": ["0", "10"],
	  "items": [
	    {"id":"a","weight":"6"}, {"id":"b","weight":"5"},
	    {"id":"c","weight":"5"}, {"id":"d","weight":"4"}
	  ]
	}`)
	if sol.BinsUsed != 2 {
		t.Fatalf("bins_used = %d, want 2", sol.BinsUsed)
	}
}

// Secondary objective: the 30-weight item must share a bin with a 10
// (three 10s cannot share a bin with only 2 slots), so the best possible
// max eccentricity is |2.5 - 5| = 5/2, achieved by the other bin {10,10}
// sitting exactly at the center.
func TestSecondaryObjective(t *testing.T) {
	_, sol := mustSolve(t, `{
	  "capacity": "40",
	  "positions": ["0", "10"],
	  "safe_interval": ["0", "10"],
	  "items": [
	    {"id":"heavy","weight":"30"}, {"id":"a","weight":"10"},
	    {"id":"b","weight":"10"}, {"id":"c","weight":"10"}
	  ]
	}`)
	if sol.BinsUsed != 2 {
		t.Fatalf("bins_used = %d, want 2", sol.BinsUsed)
	}
	if sol.MaxEccentricity != "5/2" {
		t.Fatalf("max_eccentricity = %q, want 5/2", sol.MaxEccentricity)
	}
}

// A single item heavier than the capacity makes the instance infeasible.
func TestInfeasibleCapacity(t *testing.T) {
	in := mustParse(t, `{
	  "capacity": "10",
	  "positions": ["0", "10"],
	  "safe_interval": ["0", "10"],
	  "items": [{"id":"a","weight":"11"}]
	}`)
	if _, err := Solve(in); err != ErrInfeasible {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
}

// No placement can put the CoG of either bin inside [4, 6].
func TestInfeasibleCoG(t *testing.T) {
	in := mustParse(t, `{
	  "capacity": "30",
	  "positions": ["0", "10"],
	  "safe_interval": ["4", "6"],
	  "items": [{"id":"a","weight":"10"}, {"id":"b","weight":"20"}]
	}`)
	if _, err := Solve(in); err != ErrInfeasible {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
}

// CoG window forces a specific arrangement; check it is respected exactly.
func TestCoGWindowEnforced(t *testing.T) {
	_, sol := mustSolve(t, `{
	  "capacity": "30",
	  "positions": ["0", "10"],
	  "safe_interval": ["4", "6"],
	  "items": [{"id":"a","weight":"10"}, {"id":"b","weight":"10"}]
	}`)
	if sol.BinsUsed != 1 {
		t.Fatalf("bins_used = %d, want 1", sol.BinsUsed)
	}
	if sol.Bins[0].Cog != "5" {
		t.Fatalf("cog = %q, want 5", sol.Bins[0].Cog)
	}
}

// The verifier must catch tampered evidence.
func TestVerifyDetectsTampering(t *testing.T) {
	in, sol := mustSolve(t, `{
	  "capacity": "40",
	  "positions": ["0", "10"],
	  "safe_interval": ["0", "10"],
	  "items": [{"id":"a","weight":"30"}, {"id":"b","weight":"10"}]
	}`)
	if probs := Verify(in, sol); len(probs) > 0 {
		t.Fatalf("clean solution rejected: %v", probs)
	}
	// Move an item to a different slot without updating the reported CoG.
	bad := *sol
	bins := append([]BinReport(nil), sol.Bins...)
	bins[0].Placements = append([]Placement(nil), sol.Bins[0].Placements...)
	bins[0].Placements[0].Position = "10"
	bad.Bins = bins
	probs := Verify(in, &bad)
	if len(probs) == 0 {
		t.Fatalf("tampered solution accepted")
	}
	joined := strings.Join(probs, "\n")
	if !strings.Contains(joined, "cog") {
		t.Fatalf("expected a cog mismatch among problems, got: %v", probs)
	}
}

// More than 18 items must be rejected.
func TestTooManyItems(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"capacity":"100","positions":["0"],"safe_interval":["0","0"],"items":[`)
	for i := 0; i < 19; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":"x","weight":"1"}`)
	}
	sb.WriteString(`]}`)
	if _, err := ParseInput([]byte(sb.String())); err == nil {
		t.Fatalf("19 items accepted, want rejection")
	}
}

// A larger random-feeling instance exercising the search with 12 items.
func TestLargerInstance(t *testing.T) {
	_, sol := mustSolve(t, `{
	  "capacity": "100",
	  "positions": ["0", "10", "20", "30", "40", "50"],
	  "safe_interval": ["18", "32"],
	  "items": [
	    {"id":"i01","weight":"55"}, {"id":"i02","weight":"30"},
	    {"id":"i03","weight":"25"}, {"id":"i04","weight":"20"},
	    {"id":"i05","weight":"15"}, {"id":"i06","weight":"10"},
	    {"id":"i07","weight":"12"}, {"id":"i08","weight":"8"},
	    {"id":"i09","weight":"22"}, {"id":"i10","weight":"18"},
	    {"id":"i11","weight":"9"},  {"id":"i12","weight":"7"}
	  ]
	}`)
	if sol.BinsUsed < 3 {
		t.Fatalf("bins_used = %d, want >= 3 (total weight 231)", sol.BinsUsed)
	}
	for _, b := range sol.Bins {
		if !b.WithinSafeInterval {
			t.Fatalf("bin %d outside safe interval: %+v", b.Index, b)
		}
	}
}
