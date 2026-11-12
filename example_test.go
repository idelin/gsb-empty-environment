package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0519"
)

// 策略保存前调用 Validate：只钩 openat 时，open 族其余调用会被报告为未覆盖。
func ExampleValidate() {
	policy := netsec.Policy{
		Name: "partial-openat",
		Hooks: []netsec.Hook{{
			Call:   "openat",
			Params: []netsec.Param{{Name: "path", Type: netsec.ParamString}},
			Selectors: []netsec.Selector{{
				Param:  "path",
				Prefix: "/srv/secret",
			}},
			Action: netsec.ActionSigkill,
		}},
	}
	targets := []netsec.Target{{
		Path:   "/srv/secret/data",
		Access: netsec.AccessOpen,
	}}

	report := netsec.Validate(policy, targets, netsec.DefaultFamilyTable)

	fmt.Printf("rejected=%v\n", report.Rejected)
	for _, coverage := range report.Coverage {
		fmt.Printf("covered=%v uncovered=%v normalized=%q\n",
			coverage.Covered, coverage.Uncovered, coverage.Normalized)
	}
	for _, finding := range report.Findings {
		if finding.Code == netsec.FindingUncoveredCall {
			fmt.Printf("%s: %s\n", finding.Code, finding.Call)
		}
	}

	// Output:
	// rejected=true
	// covered=[openat] uncovered=[creat open openat2] normalized="/srv/secret/data"
	// UNCOVERED_CALL: open
	// UNCOVERED_CALL: openat2
	// UNCOVERED_CALL: creat
}
