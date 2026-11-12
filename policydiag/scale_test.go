package policydiag

import "testing"

func TestEighteenRuleFullEnumeration(t *testing.T) {
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
		Requirements: []Requirement{
			{ID: "avail", Kind: ReqAvailability, Operations: []string{"decrypt"}, SubjectKey: "k-bob", FileID: "f-secret"},
			{ID: "no-unauth", Kind: ReqNoUnauthorized},
		},
	}
	for i := 0; i < 18; i++ {
		rule := Rule{ID: ruleName(i), Kind: KindAllow, Operations: []string{"decrypt"}}
		if i == 0 {
			rule.MinClearance = "secret"
			rule.FileLevels = []string{"secret"}
		} else {
			rule.SubjectKeys = []string{"k-dave"}
		}
		p.Rules = append(p.Rules, rule)
	}

	report, err := Diagnose(p, Options{})
	if err != nil {
		t.Fatalf("Diagnose error: %v", err)
	}
	if !report.Complete || report.BudgetExhausted {
		t.Fatalf("18-rule full enumeration must complete: %#v", report)
	}
	if report.ChecksUsed > 1<<18 {
		t.Fatalf("enumeration exceeded 2^18 subsets: %d", report.ChecksUsed)
	}
	if !report.AliasAudit.PropertyHolds {
		t.Fatalf("anti-alias property must hold at full scale")
	}
}

func ruleName(i int) string {
	return "r-" + string(rune('a'+i/10)) + string(rune('a'+i%10))
}
