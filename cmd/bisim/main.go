// Command bisim demonstrates coarsest bisimulation partitioning on a small
// automaton: it prints the partition, the old->new state mapping, and the
// quotient graph, then cross-checks everything against the directly computed
// greatest fixed point of the bisimulation relation.
package main

import (
	"fmt"
	"os"

	"bisim/automata"
)

func main() {
	// States 0..5. Outputs: "acc" accepting, "rej" rejecting.
	// 0 and 1 are behavioral twins; 2 differs only by a missing transition
	// (vs. 3, which transitions into a rejecting sink); 4,5 are a loop pair.
	a, err := automata.New(6,
		[]string{"mid", "mid", "mid", "mid", "acc", "rej"},
		[]automata.Edge{
			{From: 0, Label: "a", To: 4},
			{From: 0, Label: "b", To: 5},
			{From: 1, Label: "a", To: 4},
			{From: 1, Label: "b", To: 5},
			// 2: no "b" transition at all.
			{From: 2, Label: "a", To: 4},
			// 3: "b" goes to the rejecting sink 5.
			{From: 3, Label: "a", To: 4},
			{From: 3, Label: "b", To: 5},
			{From: 3, Label: "b", To: 5}, // parallel edge: multiplicity 2
			{From: 4, Label: "a", To: 4},
		},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	p := automata.CoarsestBisimulation(a)
	fmt.Printf("blocks:      %v\n", p.Blocks)
	fmt.Printf("mapping:     %s\n", p.Mapping())

	q := automata.QuotientGraph(a, p)
	fmt.Println("quotient:")
	for b := 0; b < p.NumBlocks(); b++ {
		fmt.Printf("  block %d (out=%s)\n", b, q.Outputs[b])
	}
	for _, e := range q.Edges {
		fmt.Printf("  %d --%s^%d--> %d\n", e.From, e.Label, e.Count, e.To)
	}

	if err := p.VerifyAgainstFixedPoint(a); err != nil {
		fmt.Fprintln(os.Stderr, "fixed-point check failed:", err)
		os.Exit(1)
	}
	if err := q.VerifyQuotient(a); err != nil {
		fmt.Fprintln(os.Stderr, "quotient check failed:", err)
		os.Exit(1)
	}
	fmt.Println("fixed-point check: partition matches greatest bisimulation relation")
}
