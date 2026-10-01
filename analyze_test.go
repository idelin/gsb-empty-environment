package main

import (
	"net/netip"
	"testing"
)

func rule(id, prio int, prefix string, proto, lo, hi int, act Action) Rule {
	return Rule{ID: id, Priority: prio, Prefix: netip.MustParsePrefix(prefix), Protocol: proto, PortLo: lo, PortHi: hi, Action: act}
}

func unreachableIDs(rep Report) map[int]bool {
	m := map[int]bool{}
	for _, u := range rep.Unreachable {
		m[u.RuleID] = true
	}
	return m
}

func findFinding(rep Report, earlier, later int) *Finding {
	for i := range rep.Findings {
		if rep.Findings[i].EarlierID == earlier && rep.Findings[i].LaterID == later {
			return &rep.Findings[i]
		}
	}
	return nil
}

func TestFullyShadowedIsUnreachable(t *testing.T) {
	rules := []Rule{
		rule(1, 10, "10.0.0.0/8", 6, 80, 80, Allow),
		rule(2, 20, "10.0.0.0/8", 6, 80, 80, Deny),
	}
	rep := Analyze(rules)
	if !unreachableIDs(rep)[2] {
		t.Fatal("rule #2 fully covered by rule #1 must be unreachable")
	}
	f := findFinding(rep, 1, 2)
	if f == nil || f.Kind != "shadowed" {
		t.Fatalf("expected shadowed finding for (1,2), got %+v", f)
	}
	if !f.Conflict {
		t.Fatal("allow vs deny on the same space must be a conflict")
	}
}

func TestPartialOverlapStaysReachable(t *testing.T) {
	rules := []Rule{
		rule(1, 10, "10.0.0.0/8", 6, 80, 80, Allow),
		rule(2, 20, "10.1.0.0/16", 6, 0, 65535, Deny),
	}
	rep := Analyze(rules)
	if unreachableIDs(rep)[2] {
		t.Fatal("partially covered rule #2 must stay reachable")
	}
	f := findFinding(rep, 1, 2)
	if f == nil || f.Kind != "overlap" {
		t.Fatalf("expected overlap finding for (1,2), got %+v", f)
	}
	if !f.Conflict {
		t.Fatal("overlapping allow/deny must be flagged as conflict")
	}
}

func TestUnionShadowing(t *testing.T) {
	rules := []Rule{
		rule(7, 70, "10.9.0.0/16", 6, 100, 200, Allow),
		rule(8, 80, "10.9.0.0/16", 6, 201, 300, Allow),
		rule(9, 90, "10.9.0.0/16", 6, 100, 300, Deny),
	}
	rep := Analyze(rules)
	if !unreachableIDs(rep)[9] {
		t.Fatal("rule #9 covered by the union of #7 and #8 must be unreachable")
	}
	// Pairwise, neither #7 nor #8 alone shadows #9: only overlaps.
	if f := findFinding(rep, 7, 9); f == nil || f.Kind != "overlap" {
		t.Fatalf("expected overlap finding for (7,9), got %+v", f)
	}
	if f := findFinding(rep, 8, 9); f == nil || f.Kind != "overlap" {
		t.Fatalf("expected overlap finding for (8,9), got %+v", f)
	}
}

func TestNoCrossFamilyMixing(t *testing.T) {
	rules := []Rule{
		rule(1, 10, "0.0.0.0/0", ProtoAny, 0, 65535, Deny),
		rule(2, 20, "2001:db8::/32", 6, 443, 443, Allow),
	}
	rep := Analyze(rules)
	if len(rep.Unreachable) != 0 || len(rep.Findings) != 0 {
		t.Fatalf("IPv4 rule must not affect IPv6 rule: %+v", rep)
	}
	// And the classifier agrees.
	c := NewClassifier(rules)
	act, matched := c.Decide(Packet{Dst: netip.MustParseAddr("2001:db8::1"), Proto: 6, DstPort: 443})
	if matched == nil || matched.ID != 2 || act != Allow {
		t.Fatalf("IPv6 packet must match IPv6 rule #2, got rule=%v act=%v", matched, act)
	}
}

func TestWitnessIsCounterexample(t *testing.T) {
	rules := []Rule{
		rule(1, 10, "10.0.0.0/8", 6, 80, 80, Allow),
		rule(2, 20, "10.1.0.0/16", 6, 0, 65535, Deny),
		rule(3, 30, "10.0.0.0/8", 6, 80, 80, Deny),
	}
	rep := Analyze(rules)
	byID := map[int]Rule{1: rules[0], 2: rules[1], 3: rules[2]}
	for _, f := range rep.Findings {
		if !byID[f.EarlierID].Matches(f.Witness) || !byID[f.LaterID].Matches(f.Witness) {
			t.Errorf("witness %s of finding %+v must match both rules", f.Witness, f)
		}
	}
	// The unreachable witness matches the dead rule but is decided by
	// an earlier rule.
	c := NewClassifier(rules)
	for _, u := range rep.Unreachable {
		if !byID[u.RuleID].Matches(u.Witness) {
			t.Errorf("witness %s must match dead rule #%d", u.Witness, u.RuleID)
		}
		_, matched := c.Decide(u.Witness)
		if matched == nil || matched.ID == u.RuleID {
			t.Errorf("witness for dead rule #%d must be captured earlier, got %v", u.RuleID, matched)
		}
	}
}

func TestPriorityOrder(t *testing.T) {
	// Rule listed first but with worse priority must not shadow.
	rules := []Rule{
		rule(1, 50, "10.0.0.0/8", 6, 80, 80, Deny),
		rule(2, 10, "10.0.0.0/8", 6, 80, 80, Allow),
	}
	rep := Analyze(rules)
	if !unreachableIDs(rep)[1] {
		t.Fatal("lower-priority rule #1 must be shadowed by higher-priority rule #2")
	}
	c := NewClassifier(rules)
	act, matched := c.Decide(Packet{Dst: netip.MustParseAddr("10.0.0.1"), Proto: 6, DstPort: 80})
	if act != Allow || matched.ID != 2 {
		t.Fatalf("priority order broken: act=%v rule=%v", act, matched)
	}
}
