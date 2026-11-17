package netsec_test

import (
	"context"
	"fmt"

	netsec "example.org/netsec/q0119"
)

func ExampleProgram_Evaluate() {
	allow := func(label string) netsec.CheckFunc {
		return func(context.Context) (netsec.CheckResult, error) {
			return netsec.CheckResult{
				Verdict:  netsec.VerdictAllow,
				Evidence: netsec.NewEvidence([]byte(label), label),
			}, nil
		}
	}
	deny := func(label string) netsec.CheckFunc {
		return func(context.Context) (netsec.CheckResult, error) {
			return netsec.CheckResult{
				Verdict:  netsec.VerdictDeny,
				Evidence: netsec.NewEvidence([]byte(label), label),
			}, nil
		}
	}

	policy := netsec.MustNewProgram("rules-2026-11-18",
		netsec.And(
			netsec.RuleRef("tls"),
			netsec.Not(netsec.RuleRef("blocklisted")),
		),
		[]netsec.Rule{
			{ID: "tls", Version: "v3", Check: allow("tls")},
			{ID: "blocklisted", Version: "v7", Check: deny("blocklisted")},
		},
	)

	trace, err := policy.Evaluate(context.Background())
	if err != nil {
		fmt.Println("evaluation failed:", err)
		return
	}
	fmt.Println(trace.Status, trace.Verdict)
	fmt.Println(trace.ExecutedRules())
	fmt.Println(policy.VerifyAllow(trace))

	// Output:
	// completed allow
	// [tls blocklisted]
	// <nil>
}
