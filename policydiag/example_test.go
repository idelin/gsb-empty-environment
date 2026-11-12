package policydiag_test

import (
	"fmt"

	"example.org/domainreissue/q003/policydiag"
)

func ExampleDiagnose() {
	p := &policydiag.Policy{
		Operations:     []string{"decrypt"},
		ClearanceOrder: []string{"public", "secret"},
		Subjects: []policydiag.Subject{
			{KeyID: "k-bob", PersonID: "bob", Clearance: "secret"},
			{KeyID: "k-dave", PersonID: "dave", Clearance: "public"},
		},
		Files: []policydiag.File{
			{FileID: "f-secret", Classification: "secret"},
		},
		Rules: []policydiag.Rule{
			{ID: "narrow", Kind: policydiag.KindAllow, Operations: []string{"decrypt"}, MinClearance: "secret"},
			{ID: "broad", Kind: policydiag.KindAllow, Operations: []string{"decrypt"}, SubjectKeys: []string{"k-dave"}},
		},
		Requirements: []policydiag.Requirement{
			{ID: "avail-bob", Kind: policydiag.ReqAvailability, Operations: []string{"decrypt"}, SubjectKey: "k-bob", FileID: "f-secret"},
			{ID: "no-unauth", Kind: policydiag.ReqNoUnauthorized, Operations: []string{"decrypt"}},
		},
	}

	report, err := policydiag.Diagnose(p, policydiag.Options{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("complete:", report.Complete)
	for _, group := range report.ConflictGroups {
		fmt.Println("conflict:", group.RuleIDs, "minimal:", group.Minimal)
		for _, outcome := range group.Witness.Availability {
			fmt.Println("witness:", outcome.SubjectKey, outcome.FileID, "granted:", outcome.Granted)
		}
	}
	// Output:
	// complete: true
	// conflict: [broad] minimal: true
	// witness: k-bob f-secret granted: true
}
