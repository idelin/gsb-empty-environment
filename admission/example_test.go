package admission_test

import (
	"fmt"
	"math/big"

	"prescreen.local/ddd-first-pass-beacon/admission"
)

// Example shows a host compiling a public round-admission model, enumerating
// every deterministic rule and emitting an independently verifiable
// certificate. No private key material appears anywhere in the input.
func Example() {
	half := big.NewRat(1, 2)
	model := &admission.Model{
		Participants: []admission.Participant{{
			Subject:         "reviewer-1",
			VisibleMessages: []admission.MessageType{"prefix"},
		}},
		PublicMessages: []admission.MessageType{"prefix"},
		Root:           "root",
		MaxRetries:     0,
		Ideal: map[string]*big.Rat{
			"cat-A": half,
			"cat-B": half,
		},
		Nodes: []admission.Node{
			{ID: "root", Kind: admission.KindEntry, Out: []admission.Edge{{To: "coin"}}},
			{ID: "coin", Kind: admission.KindDraw, Draw: &admission.DrawSpec{
				Name:  "coin",
				Table: admission.ProbabilityTable{"a": half, "b": half},
			}, Out: []admission.Edge{
				{To: "ta", OnOutcome: "a"},
				{To: "tb", OnOutcome: "b"},
			}},
			{ID: "ta", Kind: admission.KindTerminal, Terminal: &admission.TerminalSpec{Category: "cat-A"}},
			{ID: "tb", Kind: admission.KindTerminal, Terminal: &admission.TerminalSpec{Category: "cat-B"}},
		},
	}

	analyzer, err := model.Compile()
	if err != nil {
		panic(err)
	}
	result, err := analyzer.Enumerate()
	if err != nil {
		panic(err)
	}
	cert, err := analyzer.Certificate(result)
	if err != nil {
		panic(err)
	}
	if err := cert.Verify(model); err != nil {
		panic(err)
	}

	fmt.Println("rules:", result.PolicyCount)
	fmt.Println("failure bound:", result.FailureBound.RatString())
	fmt.Println("verified:", cert.Version)
	// Output:
	// rules: 1
	// failure bound: 0
	// verified: admission-cert-v1
}
