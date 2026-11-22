package fedpolicy_test

import (
	"fmt"

	"prescreen.local/ddd-9652-20261122-v2/fedpolicy"
)

// ExampleProject shows the end-to-end import flow: declare the mapping
// transducer, the last-match old policy, the public target labels and the
// candidate (prefix, label) combinations, then read back the minimum
// projection and the rejection witnesses.
func ExampleProject() {
	oldHeader := fedpolicy.HeaderKey{Issuer: "id.old.example", Tenant: "acme"}
	newHeader := fedpolicy.HeaderKey{Issuer: "id.new.example", Tenant: "acme"}

	// Identity transducer over three segments; cycles allow unbounded paths.
	// An empty Out on an arc would model segment dropping / many-to-one maps.
	fst := fedpolicy.NewFST(0)
	for _, sym := range []string{"svc", "admin", "pub"} {
		fst.AddArc(fedpolicy.Arc{From: 0, In: sym, Out: sym, To: 0})
	}
	fst.MarkFinal(0)

	mapper := &fedpolicy.Mapper{Channels: []*fedpolicy.Channel{
		{
			Old:        oldHeader,
			New:        newHeader,
			Alphabet:   []string{"svc", "admin", "pub"},
			Transducer: fst,
		},
	}}

	// Last-match-wins: /svc is allowed broadly, /svc/admin is carved out as
	// a deny exception; /svc/pub stays allowed and carries a public label.
	policy := fedpolicy.OldPolicy{
		Default: fedpolicy.Deny,
		Rules: []fedpolicy.OrderedRule{
			{Prefix: fedpolicy.NamePrefix{Issuer: oldHeader.Issuer, Tenant: oldHeader.Tenant, Path: []string{"svc"}}, Effect: fedpolicy.Allow},
			{Prefix: fedpolicy.NamePrefix{Issuer: oldHeader.Issuer, Tenant: oldHeader.Tenant, Path: []string{"svc", "admin"}}, Effect: fedpolicy.Deny},
		},
	}
	labeler := fedpolicy.PrefixLabeler{Rules: []fedpolicy.LabelRule{
		{Prefix: fedpolicy.NamePrefix{Issuer: oldHeader.Issuer, Tenant: oldHeader.Tenant, Path: []string{"svc", "pub"}}, Label: "tier-1"},
	}}

	candidates := []fedpolicy.Candidate{
		// Broad /svc: unsafe because the /svc/admin deny exception maps under it.
		{Prefix: fedpolicy.NamePrefix{Issuer: newHeader.Issuer, Tenant: newHeader.Tenant, Path: []string{"svc"}}},
		// Narrow /svc/pub requiring the public label: every preimage qualifies.
		{Prefix: fedpolicy.NamePrefix{Issuer: newHeader.Issuer, Tenant: newHeader.Tenant, Path: []string{"svc", "pub"}}, Require: []fedpolicy.Label{"tier-1"}},
	}

	report := fedpolicy.Project(candidates, mapper, policy, labeler, []fedpolicy.Label{"tier-1"})

	for _, decision := range report.Decisions {
		if decision.Safe {
			fmt.Printf("safe: %s\n", decision.Candidate.Prefix.String())
			continue
		}
		fmt.Printf("reject(%s): %s\n", decision.Reason, decision.Candidate.Prefix.String())
		w := decision.Witnesses[0]
		fmt.Printf("  witness old path %v -> target %v (%s)\n", w.Old.Path, w.Target.Path, w.Effect)
	}
	fmt.Printf("minimum rules: %d\n", report.MinimumRuleCount)

	// Output:
	// reject(old-deny): id.new.example/acme/svc
	//   witness old path [svc admin] -> target [svc admin] (deny)
	// safe: id.new.example/acme/svc/pub
	// minimum rules: 1
}
