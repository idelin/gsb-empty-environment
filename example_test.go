package netsec_test

import (
	"fmt"

	"example.org/netsec/q0519"
)

func ExampleValidate() {
	policy := netsec.Policy{Hooks: []netsec.Hook{{
		Call: "sys_openat",
		Args: []netsec.Arg{{Index: 1, Type: "string"}},
		Selectors: []netsec.Selector{{
			ArgIndex: 1,
			Operator: netsec.OperatorPrefix,
			Value:    "/etc",
		}},
		Action: netsec.ActionSigkill,
	}}}

	report, err := netsec.Validate(
		policy,
		[]netsec.Target{netsec.FileTarget("/etc/shadow", netsec.AccessRead)},
		netsec.DefaultFamilyTable(),
	)
	fmt.Println("error:", err)
	fmt.Println("complete:", report.Complete)
	fmt.Println("uncovered calls:", len(report.Gaps))
	fmt.Println("first uncovered call:", report.Gaps[0].Call)

	// Output:
	// error: <nil>
	// complete: false
	// uncovered calls: 2
	// first uncovered call: open
}
