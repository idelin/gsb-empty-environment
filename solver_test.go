package main

import (
	"strings"
	"testing"
)

func solve(t *testing.T, js string) *Result {
	t.Helper()
	in, err := readInput(strings.NewReader(js))
	if err != nil {
		t.Fatalf("readInput: %v", err)
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return res
}

func spotOf(res *Result, id string) (string, bool) {
	for _, a := range res.Accepted {
		if a.ID == id {
			return a.Spot, true
		}
	}
	return "", false
}

func rejectedIDs(res *Result) map[string]bool {
	m := make(map[string]bool)
	for _, r := range res.Rejected {
		m[r.ID] = true
	}
	return m
}

// Left-closed right-open: [0,60) and [60,120) do NOT conflict and may
// share one spot.
func TestHalfOpenAdjacency(t *testing.T) {
	res := solve(t, `{
	  "spots": [{"id":"A1","vehicle_types":["sedan"]}],
	  "reservations": [
	    {"id":"R1","vehicle_type":"sedan","start":0,"end":60,"profit":10},
	    {"id":"R2","vehicle_type":"sedan","start":60,"end":120,"profit":10}
	  ]
	}`)
	if res.Status != "optimal" || len(res.Accepted) != 2 {
		t.Fatalf("expected both accepted, got %+v", res)
	}
	s1, _ := spotOf(res, "R1")
	s2, _ := spotOf(res, "R2")
	if s1 != "A1" || s2 != "A1" {
		t.Fatalf("expected both on A1, got %s %s", s1, s2)
	}
	if res.TotalProfit != 20 {
		t.Fatalf("profit = %d, want 20", res.TotalProfit)
	}
}

// Overlapping pair on one spot: higher profit wins, loser gets evidence.
func TestConflictPicksHigherProfit(t *testing.T) {
	res := solve(t, `{
	  "spots": [{"id":"A1","vehicle_types":["sedan"]}],
	  "reservations": [
	    {"id":"LOW","vehicle_type":"sedan","start":0,"end":100,"profit":10},
	    {"id":"HIGH","vehicle_type":"sedan","start":50,"end":80,"profit":50}
	  ]
	}`)
	if _, ok := spotOf(res, "HIGH"); !ok {
		t.Fatalf("HIGH must be accepted: %+v", res)
	}
	if !rejectedIDs(res)["LOW"] {
		t.Fatalf("LOW must be rejected: %+v", res)
	}
	if res.TotalProfit != 50 {
		t.Fatalf("profit = %d, want 50", res.TotalProfit)
	}
	var low *Rejection
	for i := range res.Rejected {
		if res.Rejected[i].ID == "LOW" {
			low = &res.Rejected[i]
		}
	}
	if low == nil || len(low.Conflicts) != 1 || low.Conflicts[0].With != "HIGH" {
		t.Fatalf("missing conflict evidence: %+v", low)
	}
	c := low.Conflicts[0]
	if c.Spot != "A1" || c.OverlapFrom != 50 || c.OverlapTo != 80 {
		t.Fatalf("bad evidence: %+v", c)
	}
}

// Confirmed reservations are kept even when an optional one pays more.
func TestConfirmedNotWithdrawn(t *testing.T) {
	res := solve(t, `{
	  "spots": [{"id":"A1","vehicle_types":["sedan"]}],
	  "reservations": [
	    {"id":"KEEP","vehicle_type":"sedan","start":0,"end":100,"profit":1,"confirmed":true},
	    {"id":"RICH","vehicle_type":"sedan","start":10,"end":90,"profit":999}
	  ]
	}`)
	if _, ok := spotOf(res, "KEEP"); !ok {
		t.Fatalf("confirmed KEEP must be accepted: %+v", res)
	}
	if !rejectedIDs(res)["RICH"] {
		t.Fatalf("RICH must be rejected: %+v", res)
	}
}

// Buffers extend occupancy: [0,60) with 10/10 buffers blocks [70,120).
func TestBuffersExtendOccupancy(t *testing.T) {
	res := solve(t, `{
	  "defaults": {"entry_buffer":10,"exit_buffer":10},
	  "spots": [{"id":"A1","vehicle_types":["sedan"]}],
	  "reservations": [
	    {"id":"R1","vehicle_type":"sedan","start":0,"end":60,"profit":100},
	    {"id":"R2","vehicle_type":"sedan","start":70,"end":120,"profit":1}
	  ]
	}`)
	if !rejectedIDs(res)["R2"] {
		t.Fatalf("R2 must be rejected due to buffers: %+v", res)
	}
	for _, a := range res.Accepted {
		if a.ID == "R1" && (a.OccupyFrom != -10 || a.OccupyTo != 70) {
			t.Fatalf("bad occupancy window: %+v", a)
		}
	}
}

// Vehicle type compatibility restricts usable spots.
func TestVehicleTypeCompatibility(t *testing.T) {
	res := solve(t, `{
	  "spots": [
	    {"id":"SMALL","vehicle_types":["sedan"]},
	    {"id":"BIG","vehicle_types":["sedan","truck"]}
	  ],
	  "reservations": [
	    {"id":"T1","vehicle_type":"truck","start":0,"end":60,"profit":30},
	    {"id":"S1","vehicle_type":"sedan","start":0,"end":60,"profit":10},
	    {"id":"S2","vehicle_type":"sedan","start":0,"end":60,"profit":10}
	  ]
	}`)
	if len(res.Accepted) != 2 || res.TotalProfit != 40 {
		t.Fatalf("expected 2 accepted / profit 40: %+v", res)
	}
	if sp, _ := spotOf(res, "T1"); sp != "BIG" {
		t.Fatalf("truck must use BIG, got %q", sp)
	}
}

// Two confirmed orders, one spot, overlapping: infeasible with evidence.
func TestInfeasibleConfirmed(t *testing.T) {
	res := solve(t, `{
	  "spots": [{"id":"A1","vehicle_types":["sedan"]}],
	  "reservations": [
	    {"id":"C1","vehicle_type":"sedan","start":0,"end":100,"profit":1,"confirmed":true},
	    {"id":"C2","vehicle_type":"sedan","start":50,"end":150,"profit":1,"confirmed":true}
	  ]
	}`)
	if res.Status != "infeasible" {
		t.Fatalf("expected infeasible, got %+v", res)
	}
	if len(res.Rejected) != 1 || len(res.Rejected[0].Conflicts) != 1 {
		t.Fatalf("expected one rejected with one conflict: %+v", res.Rejected)
	}
}

// No compatible spot: optional order rejected with reason.
func TestNoCompatibleSpot(t *testing.T) {
	res := solve(t, `{
	  "spots": [{"id":"A1","vehicle_types":["sedan"]}],
	  "reservations": [
	    {"id":"B1","vehicle_type":"bus","start":0,"end":60,"profit":10}
	  ]
	}`)
	r := res.Rejected[0]
	if r.ID != "B1" || !strings.Contains(r.Reason, "no spot accepts") {
		t.Fatalf("bad rejection: %+v", r)
	}
}

// Limit of 25 reservations enforced.
func TestMaxReservations(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"spots":[{"id":"A1","vehicle_types":["sedan"]}],"reservations":[`)
	for i := 0; i < 26; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"R","vehicle_type":"sedan","start":0,"end":1,"profit":1}`)
	}
	b.WriteString(`]}`)
	in, err := readInput(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("readInput: %v", err)
	}
	if _, err := Solve(in); err == nil {
		t.Fatal("expected error for 26 reservations")
	}
}
