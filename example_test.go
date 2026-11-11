package netsec

import "fmt"

func ExampleExplain() {
	baseline := []RuleConfig{
		{RuleID: "generalize_age", Enabled: false},
		{RuleID: "drop_zip", Enabled: false},
		{RuleID: "suppress_job", Enabled: false},
	}
	target := []RuleConfig{
		{RuleID: "generalize_age", Enabled: true},
		{RuleID: "drop_zip", Enabled: true},
		{RuleID: "suppress_job", Enabled: true},
	}

	// Deterministic callback: in this toy model exactly two specific rules
	// together shrink the candidate set to a single person.
	evaluate := func(config []RuleConfig) (AnonymityReport, error) {
		enabled := configMask(config)
		if enabled == 0b011 {
			return AnonymityReport{CandidateSetSize: 1}, nil
		}
		return AnonymityReport{CandidateSetSize: 9}, nil
	}

	report, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   target,
		Evaluate: evaluate,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("subsets:", report.TotalSubsets)
	fmt.Println("reproducing:", report.ReproducingSubsets)
	for _, set := range report.MinimalSets {
		fmt.Println("minimal rules:", report.RuleIDs(set.Mask))
		for _, evidence := range set.DeletionProof {
			fmt.Printf("without %s -> size %d, identified %t\n",
				evidence.RuleID, evidence.CandidateSetSize, evidence.Identified)
		}
	}
	fmt.Println(report.Invocations[3])
	// Output:
	// subsets: 8
	// reproducing: 1
	// minimal rules: [generalize_age drop_zip]
	// without generalize_age -> size 9, identified false
	// without drop_zip -> size 9, identified false
	// call 3 mask=0x03 candidate_set_size=1 identified=true
}
