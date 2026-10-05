// Command hgsolve reads a hypergraph derivation problem as JSON and prints
// the derivable set plus, for each requested target, the exact minimum-cost
// proof DAG with lower/upper bounds.
//
// Input (stdin or -input file):
//
//	{
//	  "facts":   ["a", "b"],
//	  "edges":   [{"id": "e1", "from": ["a", "b"], "to": "c", "cost": 5}],
//	  "targets": ["c"]            // optional; defaults to all derivable nodes
//	}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"hgsolve"
)

type input struct {
	Facts   []string       `json:"facts"`
	Edges   []hgsolve.Edge `json:"edges"`
	Targets []string       `json:"targets"`
}

type targetResult struct {
	Target    string   `json:"target"`
	Reachable bool     `json:"reachable"`
	Cost      float64  `json:"cost"`
	Proof     []string `json:"proof,omitempty"`
	Lower     float64  `json:"lower"`
	Upper     float64  `json:"upper"`
	Expanded  int      `json:"expanded"`
}

type output struct {
	Derivable []string       `json:"derivable"`
	Targets   []targetResult `json:"targets"`
}

func main() {
	inPath := flag.String("input", "", "input JSON file (default: stdin)")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var in input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fatal(fmt.Errorf("decoding input: %w", err))
	}

	g := hgsolve.NewGraph(in.Edges)
	derivable := g.Derivable(in.Facts)

	targets := in.Targets
	if len(targets) == 0 {
		targets = derivable
	}

	out := output{Derivable: derivable}
	for _, t := range targets {
		tr := targetResult{Target: t}
		if lb, ok := g.OrLower(in.Facts, t); ok {
			tr.Lower = lb
		}
		ub := -1.0
		if c, _, ok := g.GreedyProof(in.Facts, t); ok {
			ub = c
		}
		if c, ok := g.TreeCost(in.Facts, t); ok && (ub < 0 || c < ub) {
			ub = c
		}
		res, ok := g.SolveExact(in.Facts, t)
		if !ok {
			tr.Reachable = false
			tr.Cost = -1
			tr.Upper = -1
		} else {
			tr.Reachable = true
			tr.Cost = res.Cost
			tr.Proof = res.EdgeIDs
			tr.Expanded = res.Expanded
			if ub < 0 || res.Cost < ub {
				ub = res.Cost
			}
			tr.Upper = ub
		}
		out.Targets = append(out.Targets, tr)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "hgsolve:", err)
	os.Exit(1)
}
