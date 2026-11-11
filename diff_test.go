package netsec

import "testing"

func newTestUniverse(t *testing.T) *Universe {
	t.Helper()
	u, err := NewUniverse(
		[]string{"alice", "bob", "svc"},
		[]string{"read", "write", "delete"},
		[]string{"bucket", "queue", "kms"},
		TagDomain{Key: "env", Values: []string{"dev", "prod"}},
	)
	if err != nil {
		t.Fatalf("NewUniverse: %v", err)
	}
	return u
}

func collectAll(t *testing.T, pager *Pager) ([]Finding, []Rect) {
	t.Helper()
	var findings []Finding
	var lastRemainder []Rect
	complete := false
	for !complete {
		report := pager.Next()
		findings = append(findings, report.Findings...)
		lastRemainder = report.Remainder
		complete = report.Complete
	}
	return findings, lastRemainder
}

func TestDiffNewlyOpenedOperation(t *testing.T) {
	u := newTestUniverse(t)

	oldPolicy := Policy{Name: "old", Rules: []Rule{
		{ID: "old-read", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"read"}}},
	}}
	newPolicy := Policy{Name: "new", Rules: []Rule{
		{ID: "new-read", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"read"}}},
		{ID: "new-write", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"write"}}},
	}}

	pager, err := Diff(u, oldPolicy, newPolicy, 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	findings, remainder := collectAll(t, pager)
	if remainder != nil {
		t.Fatalf("complete report must carry no remainder, got %d rects", len(remainder))
	}

	if len(findings) != 1 {
		t.Fatalf("want 1 finding rectangle, got %d: %#v", len(findings), findings)
	}
	finding := findings[0]
	if got := finding.Rect.Values[ActionAxis]; len(got) != 1 || got[0] != "write" {
		t.Fatalf("finding action = %v, want [write]", got)
	}
	if finding.Points != 3*3*2 {
		t.Fatalf("finding points = %d, want %d", finding.Points, 3*3*2)
	}
	if len(finding.NewHits) != 1 || finding.NewHits[0].RuleID != "new-write" {
		t.Fatalf("new hits = %#v, want new-write only", finding.NewHits)
	}
	if len(finding.OldHits) != 0 {
		t.Fatalf("old hits = %#v, want none (default deny)", finding.OldHits)
	}

	verification, err := Verify(u, oldPolicy, newPolicy, findings, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK {
		t.Fatalf("verification failed: %#v", verification)
	}
}

func TestDiffDenyPrecedence(t *testing.T) {
	u := newTestUniverse(t)

	oldPolicy := Policy{Name: "old", Rules: []Rule{
		{ID: "allow-all", Effect: Allow, Condition: In{Axis: PrincipalAxis, Values: []string{"alice", "bob", "svc"}}},
		{ID: "deny-prod", Effect: Deny, Condition: In{Axis: TagAxis, Values: []string{"prod"}}},
	}}
	newPolicy := Policy{Name: "new", Rules: []Rule{
		{ID: "allow-all", Effect: Allow, Condition: In{Axis: PrincipalAxis, Values: []string{"alice", "bob", "svc"}}},
	}}

	pager, err := Diff(u, oldPolicy, newPolicy, 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	findings, _ := collectAll(t, pager)

	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %#v", findings)
	}
	if got := findings[0].Rect.Values[TagAxis]; len(got) != 1 || got[0] != "prod" {
		t.Fatalf("finding env = %v, want [prod]", got)
	}
	hasDeny := false
	for _, hit := range findings[0].OldHits {
		if hit.RuleID == "deny-prod" && hit.Effect == Deny {
			hasDeny = true
		}
	}
	if !hasDeny {
		t.Fatalf("old hits %#v must contain deny-prod", findings[0].OldHits)
	}

	verification, err := Verify(u, oldPolicy, newPolicy, findings, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK || verification.TargetPoints != 3*3*3 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestDiffSemanticEquivalence(t *testing.T) {
	u := newTestUniverse(t)

	base := []Rule{
		{ID: "r1", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"read"}}},
		{ID: "r2", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"write"}}},
	}
	reworded := Policy{Name: "reworded", Rules: []Rule{
		{ID: "rw", Effect: Allow, Condition: Or{Any: []Condition{
			In{Axis: ActionAxis, Values: []string{"write"}},
			In{Axis: ActionAxis, Values: []string{"read"}},
		}}},
	}}

	pager, err := Diff(u, reworded, Policy{Name: "reordered", Rules: []Rule{base[1], base[0]}}, 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	findings, _ := collectAll(t, pager)
	if len(findings) != 0 {
		t.Fatalf("semantically equal policies must not produce findings, got %#v", findings)
	}

	verification, err := Verify(u, reworded, Policy{Name: "reordered", Rules: []Rule{base[1], base[0]}}, nil, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK || verification.TargetPoints != 0 {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestDiffBudgetPaging(t *testing.T) {
	u, err := NewUniverse(
		[]string{"a", "b"},
		[]string{"act1", "act2"},
		[]string{"res1", "res2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	oldPolicy := Policy{Name: "old"}
	newPolicy := Policy{Name: "new", Rules: []Rule{
		// Two disjoint corners: (a, act1) and (b, act2); they cannot merge
		// because both the principal and action axes differ.
		{ID: "corner-1", Effect: Allow, Condition: And{All: []Condition{
			In{Axis: PrincipalAxis, Values: []string{"a"}},
			In{Axis: ActionAxis, Values: []string{"act1"}},
		}}},
		{ID: "corner-2", Effect: Allow, Condition: And{All: []Condition{
			In{Axis: PrincipalAxis, Values: []string{"b"}},
			In{Axis: ActionAxis, Values: []string{"act2"}},
		}}},
	}}

	pager, err := Diff(u, oldPolicy, newPolicy, 1)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	first := pager.Next()
	if first.Complete {
		t.Fatalf("first page must not be complete")
	}
	if len(first.Findings) != 1 {
		t.Fatalf("first page findings = %d, want 1", len(first.Findings))
	}
	if len(first.Remainder) == 0 {
		t.Fatalf("first page must expose a non-empty remainder")
	}

	rest := pager.Next()
	if !rest.Complete {
		t.Fatalf("second page must be complete")
	}
	if rest.Remainder != nil {
		t.Fatalf("complete page remainder must be nil")
	}

	all := append(append([]Finding{}, first.Findings...), rest.Findings...)
	verification, err := Verify(u, oldPolicy, newPolicy, all, first.Remainder)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK {
		t.Fatalf("verification = %#v", verification)
	}
	if verification.TargetPoints != 4 || verification.FindingsPoints != 4 {
		t.Fatalf("verification = %#v", verification)
	}

	again := pager.Next()
	if !again.Complete || len(again.Findings) != 0 {
		t.Fatalf("exhausted pager = %#v", again)
	}
}

func TestShrunkUniverseVerification(t *testing.T) {
	full := newTestUniverse(t)

	oldPolicy := Policy{Name: "old", Rules: []Rule{
		{ID: "read-only", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"read"}}},
	}}
	newPolicy := Policy{Name: "new", Rules: []Rule{
		{ID: "rw", Effect: Allow, Condition: In{Axis: ActionAxis, Values: []string{"read", "write"}}},
	}}

	shrunk, err := full.Shrunk([][]string{
		{"alice", "svc"},
		{"read", "write", "delete"},
		{"bucket", "kms"},
		{"dev", "prod"},
	})
	if err != nil {
		t.Fatalf("Shrunk: %v", err)
	}
	if shrunk.Size() != 2*3*2*2 {
		t.Fatalf("shrunk size = %d", shrunk.Size())
	}

	pager, err := Diff(shrunk, oldPolicy, newPolicy, 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	findings, _ := collectAll(t, pager)
	verification, err := Verify(shrunk, oldPolicy, newPolicy, findings, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK {
		t.Fatalf("verification = %#v", verification)
	}
	if verification.TargetPoints != 2*2*2 {
		t.Fatalf("target points = %d", verification.TargetPoints)
	}
}

func TestBooleanConditions(t *testing.T) {
	u := newTestUniverse(t)

	oldPolicy := Policy{Name: "old", Rules: []Rule{
		{ID: "deny-all", Effect: Deny, Condition: In{Axis: PrincipalAxis, Values: []string{"alice", "bob", "svc"}}},
	}}
	newPolicy := Policy{Name: "new", Rules: []Rule{
		{ID: "alice-dev-read", Effect: Allow, Condition: And{All: []Condition{
			In{Axis: PrincipalAxis, Values: []string{"alice"}},
			In{Axis: ActionAxis, Values: []string{"read"}},
			In{Axis: TagAxis, Values: []string{"dev"}},
		}}},
		{ID: "bob-not-delete", Effect: Allow, Condition: And{All: []Condition{
			In{Axis: PrincipalAxis, Values: []string{"bob"}},
			Not{Inner: In{Axis: ActionAxis, Values: []string{"delete"}}},
		}}},
	}}

	pager, err := Diff(u, oldPolicy, newPolicy, 0)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	findings, _ := collectAll(t, pager)
	verification, err := Verify(u, oldPolicy, newPolicy, findings, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !verification.OK {
		t.Fatalf("verification = %#v", verification)
	}
	if verification.TargetPoints != 3+12 {
		t.Fatalf("target points = %d, want %d", verification.TargetPoints, 3+12)
	}
}

func TestInvalidInputs(t *testing.T) {
	if _, err := NewUniverse([]string{"a"}, nil, []string{"r"}); err == nil {
		t.Fatal("empty action domain must fail")
	}
	u, err := NewUniverse([]string{"a"}, []string{"x"}, []string{"r"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Diff(u,
		Policy{Rules: []Rule{{ID: "bad", Effect: Allow, Condition: In{Axis: 9, Values: []string{"x"}}}}},
		Policy{}, 0); err == nil {
		t.Fatal("out-of-range axis must fail")
	}
	if _, err := Diff(u,
		Policy{Rules: []Rule{{ID: "bad", Effect: "maybe", Condition: In{Axis: 0, Values: []string{"a"}}}}},
		Policy{}, 0); err == nil {
		t.Fatal("invalid effect must fail")
	}
	if _, err := Diff(u,
		Policy{Rules: []Rule{
			{ID: "x", Effect: Allow, Condition: In{Axis: 0, Values: []string{"a"}}},
			{ID: "x", Effect: Allow, Condition: In{Axis: 0, Values: []string{"a"}}},
		}}, Policy{}, 0); err == nil {
		t.Fatal("duplicate rule ids must fail")
	}
}
