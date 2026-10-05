// Command cyclecut reads a dependency graph (JSON) and prints the cheapest
// set of authorized edges to cut (each with its designated bootstrap
// artifact) plus a topological build plan for the remaining acyclic graph.
//
// Usage:
//
//	cyclecut -f graph.json [-format text|json]
//	cat graph.json | cyclecut
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"cyclecut/internal/solver"
)

func main() {
	file := flag.String("f", "", "input graph JSON file (default: stdin)")
	format := flag.String("format", "text", "output format: text or json")
	flag.Parse()

	var data []byte
	var err error
	if *file != "" {
		data, err = ioutil.ReadFile(*file)
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("read input: %v", err)
	}

	var g solver.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		fatal("parse input JSON: %v", err)
	}

	plan, err := solver.Solve(&g)
	if err != nil {
		fatal("invalid graph: %v", err)
	}

	switch *format {
	case "json":
		out, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			fatal("encode plan: %v", err)
		}
		fmt.Println(string(out))
	case "text":
		printText(plan)
	default:
		fatal("unknown format %q (want text or json)", *format)
	}

	if !plan.Feasible {
		os.Exit(1)
	}
}

func printText(p *solver.Plan) {
	if !p.Feasible {
		fmt.Println("Status: INFEASIBLE")
		fmt.Printf("Reason: %s\n", p.Reason)
		return
	}
	fmt.Println("Status: feasible")
	fmt.Printf("Total cost: %d\n", p.TotalCost)
	fmt.Printf("Cuts (%d):\n", len(p.Cuts))
	for _, c := range p.Cuts {
		fmt.Printf("  - edge #%d %s -> %s  artifact=%s  cost=%d\n", c.EdgeID, c.From, c.To, c.Artifact, c.Cost)
	}
	fmt.Println("Topological plan:")
	for i, n := range p.TopoOrder {
		fmt.Printf("  %d. %s\n", i+1, n)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "cyclecut: "+format+"\n", args...)
	os.Exit(2)
}
