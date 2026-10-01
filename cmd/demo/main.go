// Command demo walks through the acceptance scenario of the reasoner:
// forward chaining, minimal explanations, and cascading retraction.
package main

import (
	"fmt"

	"reasoner"
)

func main() {
	e := reasoner.New(
		&reasoner.Rule{Name: "r1", Premises: []reasoner.Fact{"A"}, Conclusion: "B"},
		&reasoner.Rule{Name: "r2", Premises: []reasoner.Fact{"C"}, Conclusion: "B"},
		&reasoner.Rule{Name: "r3", Premises: []reasoner.Fact{"B"}, Conclusion: "D"},
	)

	fmt.Println("== assert A ==")
	e.Assert("A")
	fmt.Println("known facts:", e.Facts())
	showProof(e, "D")

	fmt.Println("== assert C (B gains independent support) ==")
	e.Assert("C")
	fmt.Println("known facts:", e.Facts())

	fmt.Println("== retract A (B survives via C) ==")
	must(e.Retract("A"))
	fmt.Println("known facts:", e.Facts())
	showProof(e, "B")

	fmt.Println("== retract C (B and D lose all support) ==")
	must(e.Retract("C"))
	fmt.Println("known facts:", e.Facts())
}

func showProof(e *reasoner.Engine, f reasoner.Fact) {
	proof, err := e.Explain(f)
	if err != nil {
		fmt.Println("explain", f, ":", err)
		return
	}
	fmt.Printf("minimal proof for %s (depth %d):\n%s", f, proof.Depth(), proof)
	fmt.Println("evidence chain:")
	for i, s := range proof.Steps() {
		fmt.Printf("  %d. %s\n", i+1, s)
	}
	fmt.Println("base facts:", proof.BaseFacts())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
