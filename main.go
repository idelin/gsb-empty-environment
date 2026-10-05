package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <original.seq> <candidate.seq>\n", os.Args[0])
		os.Exit(2)
	}
	orig, err := loadProgram(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "original: %v\n", err)
		os.Exit(2)
	}
	cand, err := loadProgram(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "candidate: %v\n", err)
		os.Exit(2)
	}

	res := Check(orig, cand)

	if res.PermErr != "" {
		fmt.Printf("ILLEGAL: not a reordering: %s\n", res.PermErr)
		printSim(res.Sim)
		os.Exit(1)
	}

	if len(res.Violations) > 0 {
		v := res.Violations[0].Edge
		fmt.Printf("ILLEGAL: earliest violated dependence: %s on %s: #%d %q must stay before #%d %q",
			strings.Join(v.Kinds, "+"), v.Loc, v.Src, orig[v.Src].Text, v.Dst, orig[v.Dst].Text)
		if len(res.Violations) > 1 {
			fmt.Printf(" (and %d more violated edge(s) of %d total)", len(res.Violations)-1, res.Edges)
		}
		fmt.Println()
		printSim(res.Sim)
		os.Exit(1)
	}

	fmt.Printf("LEGAL: all %d dependence edge(s) preserved across %d instructions\n",
		res.Edges, len(orig))
	printSim(res.Sim)
	if res.Sim.Mismatch != nil {
		// Dependence-complete reordering must be semantics-preserving;
		// a divergence here indicates an internal inconsistency.
		fmt.Println("WARNING: simulation diverged despite preserved dependences")
		os.Exit(1)
	}
}

func loadProgram(path string) ([]*Instr, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseProgram(string(data))
}

func printSim(sim SimResult) {
	switch {
	case sim.Skipped:
		fmt.Println("simulation: skipped (state space too large to track)")
	case sim.Mismatch != nil:
		m := sim.Mismatch
		fmt.Printf("simulation: counterexample after %d %s state(s): init {%s} -> %s: original=%d candidate=%d\n",
			sim.States, sim.Mode, m.Init, m.Cell, m.OrigVal, m.CandVal)
	default:
		fmt.Printf("simulation: no divergence in %d %s state(s) (agreement is not a proof of legality)\n",
			sim.States, sim.Mode)
	}
}
