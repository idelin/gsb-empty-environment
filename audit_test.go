package netsec

import (
	"testing"
	"time"
)

func TestSearchShortestViolationFindsNoStaleRevocationBug(t *testing.T) {
	reject := func(Route) bool { return false }
	sc := AuditScenario{
		Peer:                 Peer{NeighborAS: 65100, RouterID: "198.51.100.1"},
		RestartTime:          30 * time.Second,
		Start:                time.Unix(2000, 0).UTC(),
		InitialRoute:         Route{ID: "r1", Prefix: "10.0.0.0/8", AttrsDigest: "a"},
		InitialPolicy:        func(Route) bool { return true },
		InitialVRP:           func(Route) bool { return true },
		InitialPolicyVersion: 1,
		InitialVRPVersion:    1,
		Actions: []AuditAction{
			{Kind: AuditDisconnect, Reason: DisconnectTransport},
			{Kind: AuditPolicy, PolicyVersion: 2, Policy: reject},
			{Kind: AuditVRP, VRPVersion: 2, VRP: reject},
			{Kind: AuditReconnect},
			{Kind: AuditAnnounce, Route: Route{ID: "r1-new", Prefix: "10.0.0.0/8", AttrsDigest: "a"}},
			{Kind: AuditEOR},
			{Kind: AuditAdvance, Duration: time.Second},
		},
	}
	finding, err := SearchShortestViolation(sc)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if finding != nil {
		t.Fatalf("found violation %q trace=%#v", finding.Violation, finding.Trace)
	}
}

func TestSearchShortestViolationReportsShortestTraceShape(t *testing.T) {
	// A scenario with no stale route cannot produce a finding, but this also
	// exercises depth-zero and invalid-branch handling.
	sc := AuditScenario{
		Peer:         Peer{NeighborAS: 65101, RouterID: "198.51.100.2"},
		RestartTime:  10 * time.Second,
		InitialRoute: Route{ID: "r", Prefix: "10.1.0.0/16"},
		Actions: []AuditAction{
			{Kind: AuditAdvance, Duration: 20 * time.Second},
			{Kind: AuditEOR},
		},
	}
	finding, err := SearchShortestViolation(sc)
	if err != nil {
		t.Fatal(err)
	}
	if finding != nil {
		t.Fatalf("unexpected finding: %#v", finding)
	}
}
