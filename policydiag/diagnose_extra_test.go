package policydiag

import "testing"

func TestJointMinimalConflictGroupAcrossOperations(t *testing.T) {
	// A clean, self-contained policy where two over-broad allows on different
	// operations jointly violate the no-unauthorized requirement. Removing
	// either alone still leaves violations, so the minimal group contains both.
	p := &Policy{
		Operations:     []string{"decrypt", "export"},
		ClearanceOrder: []string{"public", "secret", "topsecret"},
		Subjects: []Subject{
			{KeyID: "k-bob", PersonID: "bob", Clearance: "secret"},
			{KeyID: "k-dave", PersonID: "dave", Clearance: "public"},
			{KeyID: "k-erin", PersonID: "erin", Clearance: "topsecret"},
			{KeyID: "k-finn", PersonID: "finn", Clearance: "topsecret"},
			{KeyID: "k-gina", PersonID: "gina", Clearance: "topsecret"},
		},
		Files: []File{
			{FileID: "f-secret", Classification: "secret"},
			{FileID: "f-top", Classification: "topsecret"},
		},
		Rules: []Rule{
			{ID: "narrow-decrypt", Kind: KindAllow, Operations: []string{"decrypt"}, MinClearance: "secret", FileLevels: []string{"secret"}},
			{ID: "broad-decrypt", Kind: KindAllow, Operations: []string{"decrypt"}, SubjectKeys: []string{"k-dave"}},
			{ID: "dual-export", Kind: KindDual, Operations: []string{"export"}, MinClearance: "topsecret"},
			{ID: "broad-export", Kind: KindAllow, Operations: []string{"export"}, SubjectKeys: []string{"k-dave"}},
		},
		Requirements: []Requirement{
			{ID: "avail-bob", Kind: ReqAvailability, Operations: []string{"decrypt"}, SubjectKey: "k-bob", FileID: "f-secret"},
			{ID: "avail-erin", Kind: ReqAvailability, Operations: []string{"export"}, SubjectKey: "k-erin", FileID: "f-top"},
			{ID: "no-unauth", Kind: ReqNoUnauthorized},
		},
	}
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if len(report.ConflictGroups) != 1 {
		t.Fatalf("want one joint minimal group, got %#v", report.ConflictGroups)
	}
	group := report.ConflictGroups[0]
	if len(group.RuleIDs) != 2 {
		t.Fatalf("minimal group must contain both broad rules: %#v", group.RuleIDs)
	}
	ids := map[string]bool{}
	for _, id := range group.RuleIDs {
		ids[id] = true
	}
	if !ids["broad-decrypt"] || !ids["broad-export"] {
		t.Fatalf("unexpected minimal group: %#v", group.RuleIDs)
	}
	if len(group.Witness.RemainingFailures) != 0 {
		t.Fatalf("joint removal must fully repair policy: %#v", group.Witness.RemainingFailures)
	}
}

func TestAvailabilityConflictThroughDeny(t *testing.T) {
	p := testPolicy()
	p.Rules[0].MinClearance = "secret"
	p.Rules = append(p.Rules, Rule{
		ID:          "r-block-bob-secret",
		Kind:        KindDeny,
		Operations:  []string{"decrypt"},
		SubjectKeys: []string{"key-bob-1"},
		FileIDs:     []string{"f-secret"},
	})
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	ids := map[string]bool{}
	for _, group := range report.ConflictGroups {
		if !group.Minimal {
			t.Fatalf("group must be minimal: %#v", group)
		}
		for _, id := range group.RuleIDs {
			ids[id] = true
		}
	}
	if !ids["r-allow-all-decrypt"] || !ids["r-block-bob-secret"] {
		t.Fatalf("missing expected conflict groups: %#v", ids)
	}
}

func TestBudgetExhaustionMarkedNonComplete(t *testing.T) {
	p := testPolicy()
	report, err := Diagnose(p, Options{MaxChecks: 1})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if report.Complete || !report.BudgetExhausted {
		t.Fatalf("budget exhaustion must be explicit: complete=%v exhausted=%v",
			report.Complete, report.BudgetExhausted)
	}
	for _, group := range report.ConflictGroups {
		if group.Minimal {
			t.Fatalf("groups found under a truncated enumeration must be flagged not minimal: %#v", group)
		}
	}
}

func TestSatisfiablePolicyHasNoConflict(t *testing.T) {
	p := testPolicy()
	p.Rules[0].MinClearance = "secret"
	p.Rules[0].FileLevels = []string{"secret"}
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if !report.Complete || len(report.ConflictGroups) != 0 || len(report.Unfixable) != 0 {
		t.Fatalf("policy should be conflict free: %#v", report)
	}
	if !report.AliasAudit.PropertyHolds {
		t.Fatalf("alias audit must still hold")
	}
}

func TestUnfixablePolicyReported(t *testing.T) {
	p := testPolicy()
	p.Rules = []Rule{
		{ID: "r-dual-top-export", Kind: KindDual, Operations: []string{"export"}, FileLevels: []string{"topsecret"}},
	}
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if len(report.ConflictGroups) != 0 || len(report.Unfixable) == 0 {
		t.Fatalf("expected unfixable requirements, got %#v", report)
	}
	var sawAvailability bool
	for _, failure := range report.Unfixable {
		if failure.Kind == ReqAvailability {
			sawAvailability = true
		}
	}
	if !sawAvailability {
		t.Fatalf("unfixable availability failure missing: %#v", report.Unfixable)
	}
}

func TestRuleLimitAndValidation(t *testing.T) {
	p := testPolicy()
	for i := len(p.Rules); i < 19; i++ {
		p.Rules = append(p.Rules, Rule{ID: "r-extra", Kind: KindAllow})
	}
	if _, err := Diagnose(p, Options{}); err == nil {
		t.Fatalf("19 rules must be rejected")
	}

	bad := testPolicy()
	bad.Rules[0].Operations = []string{"does-not-exist"}
	if _, err := Diagnose(bad, Options{}); err == nil {
		t.Fatalf("unknown operation reference must be rejected")
	}
}

func TestDualInfeasibleWhenOnlyAliasesAvailable(t *testing.T) {
	p := &Policy{
		Operations:     []string{"decrypt"},
		ClearanceOrder: []string{"public", "secret"},
		Subjects: []Subject{
			{KeyID: "k-a1", PersonID: "alice", Clearance: "secret"},
			{KeyID: "k-a2", PersonID: "alice", Clearance: "secret"},
		},
		Files: []File{{FileID: "f1", Classification: "secret"}},
		Rules: []Rule{
			{ID: "dual", Kind: KindDual, Operations: []string{"decrypt"}},
		},
		Requirements: []Requirement{
			{ID: "avail", Kind: ReqAvailability, Operations: []string{"decrypt"}, SubjectKey: "k-a1", FileID: "f1"},
		},
	}
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if len(report.ConflictGroups) != 0 || len(report.Unfixable) == 0 {
		t.Fatalf("alias-only world cannot satisfy dual approval: %#v", report)
	}
	if !report.AliasAudit.PropertyHolds {
		t.Fatalf("alias pair must never count as two people")
	}
	d, err := Evaluate(p, "decrypt", "k-a1", "f1")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if d.Granted || d.DualFeasible || len(d.EligibleApprovers) != 0 {
		t.Fatalf("dual approval must be infeasible with only aliases: %#v", d)
	}
}
