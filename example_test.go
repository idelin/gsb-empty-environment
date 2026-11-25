package closure_test

import (
	"fmt"

	closure "prescreen.local/ps-c690d01eea"
)

func ExampleAnalyze() {
	root := closure.Identity{Name: "root", Version: 1, Source: "example.com/root", Digest: "sha256:root"}
	lib := closure.Identity{Name: "lib", Version: 3, Source: "example.com/lib", Digest: "sha256:lib"}

	request := closure.Request{
		Root:          root,
		VariableOrder: []string{"release"},
		Packages: []closure.Package{
			{ID: root, Deps: []closure.Dependency{{
				Name:         "lib",
				Alternatives: []closure.Identity{lib},
			}}},
			{ID: lib},
		},
		Attestations: []closure.Attestation{
			{ID: root, PolicyGeneration: 1},
			// This proof applies only to release builds.
			{ID: lib, PolicyGeneration: 1, Condition: closure.Condition{"release": true}},
		},
		PolicyGeneration: 1,
		NodeBudget:       128,
	}

	report, err := closure.Analyze(request)
	if err != nil {
		panic(err)
	}

	for _, release := range []bool{true, false} {
		config := closure.Assignment{"release": release}
		fmt.Printf("release=%v: %s\n", release, report.Region(config))
	}

	for _, evidence := range report.Evidence {
		if err := closure.VerifyEvidence(request, evidence); err != nil {
			panic(err)
		}
	}
	// Output:
	// release=true: pass
	// release=false: reject
}
