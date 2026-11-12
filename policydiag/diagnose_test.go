package policydiag

import "testing"

func testPolicy() *Policy {
	return &Policy{
		Operations:     []string{"decrypt", "export"},
		ClearanceOrder: []string{"public", "internal", "secret", "topsecret"},
		Subjects: []Subject{
			{KeyID: "key-alice-1", PersonID: "alice", Roles: []string{"analyst"}, Clearance: "secret"},
			{KeyID: "key-alice-2", PersonID: "alice", Roles: []string{"analyst"}, Clearance: "topsecret"},
			{KeyID: "key-bob-1", PersonID: "bob", Roles: []string{"auditor"}, Clearance: "secret"},
			{KeyID: "key-carol-1", PersonID: "carol", Roles: []string{"admin"}, Clearance: "topsecret"},
			{KeyID: "key-erin-1", PersonID: "erin", Roles: []string{"auditor"}, Clearance: "topsecret"},
			{KeyID: "key-dave-1", PersonID: "dave", Roles: []string{"intern"}, Clearance: "internal"},
		},
		Files: []File{
			{FileID: "f-secret", Classification: "secret"},
			{FileID: "f-top", Classification: "topsecret"},
		},
		Rules: []Rule{
			{ID: "r-allow-all-decrypt", Kind: KindAllow, Operations: []string{"decrypt"}},
			{ID: "r-allow-bob-secret-decrypt", Kind: KindAllow, Operations: []string{"decrypt"}, SubjectKeys: []string{"key-bob-1"}, FileIDs: []string{"f-secret"}},
			{ID: "r-deny-intern", Kind: KindDeny, Operations: []string{"decrypt"}, SubjectRoles: []string{"intern"}},
			{ID: "r-dual-top-export", Kind: KindDual, Operations: []string{"export"}, FileLevels: []string{"topsecret"}},
			{ID: "r-allow-export", Kind: KindAllow, Operations: []string{"export"}},
			{ID: "r-deny-bob-export", Kind: KindDeny, Operations: []string{"export"}, SubjectKeys: []string{"key-bob-1"}},
		},
		Requirements: []Requirement{
			{ID: "avail-bob-decrypt", Kind: ReqAvailability, Operations: []string{"decrypt"}, SubjectKey: "key-bob-1", FileID: "f-secret"},
			{ID: "avail-alice-top-export", Kind: ReqAvailability, Operations: []string{"export"}, SubjectKey: "key-alice-1", FileID: "f-top"},
			{ID: "no-unauth-decrypt", Kind: ReqNoUnauthorized, Operations: []string{"decrypt"}},
		},
	}
}

func TestAliasPairCannotDischargeDualApproval(t *testing.T) {
	p := testPolicy()
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose returned error: %v", err)
	}
	audit := report.AliasAudit
	if !audit.PropertyHolds {
		t.Fatalf("alias property violated: an alias pair was accepted as dual approval")
	}
	wantRequests := len(p.Operations) * len(p.Subjects) * len(p.Files)
	if audit.CheckedRequests != wantRequests {
		t.Fatalf("checked %d requests, want %d", audit.CheckedRequests, wantRequests)
	}
	aliases, ok := audit.PersonAliases["alice"]
	if !ok || len(aliases) != 2 {
		t.Fatalf("alice aliases not recorded: %#v", audit.PersonAliases)
	}
	var foundRejected bool
	for _, pair := range audit.RejectedAliasPairs {
		if pair.PersonID == "alice" &&
			((pair.FirstKey == "key-alice-1" && pair.SecondKey == "key-alice-2") ||
				(pair.FirstKey == "key-alice-2" && pair.SecondKey == "key-alice-1")) {
			foundRejected = true
		}
	}
	if !foundRejected {
		t.Fatalf("alice alias pair not listed as rejected: %#v", audit.RejectedAliasPairs)
	}

	pairs, err := EligibleApproverPairs(p, "key-alice-1", "f-top")
	if err != nil {
		t.Fatalf("EligibleApproverPairs error: %v", err)
	}
	for _, pair := range pairs {
		if pair.FirstPerson == pair.SecondPerson {
			t.Fatalf("same-person approver pair returned: %#v", pair)
		}
		if pair.FirstPerson == "alice" || pair.SecondPerson == "alice" {
			t.Fatalf("actor's own aliases appear as approvers: %#v", pair)
		}
	}
}

func TestDenyPrecedenceAndDualFeasibility(t *testing.T) {
	p := testPolicy()

	dave, err := Evaluate(p, "decrypt", "key-dave-1", "f-secret")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !dave.Denied || dave.Granted {
		t.Fatalf("intern decrypt must be deny-first: %#v", dave)
	}
	if len(dave.MatchedDeny) != 1 || dave.MatchedDeny[0] != "r-deny-intern" {
		t.Fatalf("expected r-deny-intern, got %#v", dave.MatchedDeny)
	}

	bobExport, err := Evaluate(p, "export", "key-bob-1", "f-secret")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !bobExport.Denied || bobExport.Granted {
		t.Fatalf("bob export must be denied despite broad allow: %#v", bobExport)
	}

	aliceExport, err := Evaluate(p, "export", "key-alice-1", "f-top")
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if !aliceExport.Granted || !aliceExport.DualRequired || !aliceExport.DualFeasible {
		t.Fatalf("alice top export needs feasible dual approval: %#v", aliceExport)
	}
	for _, pair := range aliceExport.EligibleApprovers {
		if pair.FirstPerson == pair.SecondPerson {
			t.Fatalf("alias pair in approvers: %#v", pair)
		}
	}
}

func TestMinimalConflictGroupsForOverBroadAllow(t *testing.T) {
	p := testPolicy()
	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if !report.Complete || report.BudgetExhausted {
		t.Fatalf("full enumeration must be complete: %#v", report)
	}
	if len(report.ConflictGroups) != 1 {
		t.Fatalf("want one minimal conflict group, got %d: %#v",
			len(report.ConflictGroups), report.ConflictGroups)
	}
	group := report.ConflictGroups[0]
	if len(group.RuleIDs) != 1 || group.RuleIDs[0] != "r-allow-all-decrypt" {
		t.Fatalf("unexpected conflict group: %#v", group.RuleIDs)
	}
	if !group.Minimal || group.Witness == nil {
		t.Fatalf("group must be proven minimal with a witness: %#v", group)
	}
	if len(group.Witness.NoUnauthorized) != 0 {
		t.Fatalf("repair witness must have no unauthorized grants: %#v",
			group.Witness.NoUnauthorized)
	}
	if len(group.Witness.RemainingFailures) != 0 {
		t.Fatalf("single removal must fully repair policy: %#v",
			group.Witness.RemainingFailures)
	}
	if len(group.Witness.Availability) != 2 {
		t.Fatalf("both availability requirements need witnesses: %#v",
			group.Witness.Availability)
	}
	for _, outcome := range group.Witness.Availability {
		if !outcome.Granted {
			t.Fatalf("availability witness not granted: %#v", outcome)
		}
		if outcome.DualRequired {
			if outcome.ApproverPair == nil ||
				outcome.ApproverPair.FirstPerson == outcome.ApproverPair.SecondPerson {
				t.Fatalf("dual witness needs distinct-person approvers: %#v", outcome)
			}
		}
	}
}
