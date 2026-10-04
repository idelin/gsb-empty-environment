package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

type inputJSON struct {
	Facts []Fact      `json:"facts"`
	Edges []Hyperedge `json:"edges"`
}

func main() {
	file := flag.String("f", "", "input JSON file (default: stdin)")
	maxStates := flag.Int("max-states", 200000, "state budget for the exact subset search")
	boundsOnly := flag.Bool("bounds-only", false, "skip the exact search, print only [lower, upper] bounds")
	flag.Parse()

	var data []byte
	var err error
	if *file != "" {
		data, err = ioutil.ReadFile(*file)
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal(err)
	}
	var in inputJSON
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(fmt.Errorf("bad input JSON: %v", err))
	}
	for _, e := range in.Edges {
		if e.Cost < 0 {
			fatal(fmt.Errorf("edge %q has negative cost", e.ID))
		}
	}
	prob := &Problem{Facts: in.Facts, Edges: in.Edges}

	res := prob.Solve(*maxStates)
	if *boundsOnly {
		res.Truncated = true
		res.Explored = 0
		for _, nr := range res.Nodes {
			nr.Exact = false
			nr.Proof = nil
		}
	}
	printReport(res)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func printReport(res *SolveResult) {
	var derivable []string
	for _, n := range res.Order {
		if res.Nodes[n].Derivable {
			derivable = append(derivable, n)
		}
	}
	fmt.Printf("Derivable set (%d): %s\n", len(derivable), strings.Join(derivable, ", "))
	if res.Truncated {
		fmt.Println("NOTE: exact search truncated; unproven nodes carry [lower, upper] bounds only.")
	}
	fmt.Printf("States explored: %d\n\n", res.Explored)

	for _, n := range res.Order {
		nr := res.Nodes[n]
		if !nr.Derivable {
			fmt.Printf("  %-10s not derivable\n", n)
			continue
		}
		status := "exact"
		costStr := fmt.Sprintf("%g", nr.Cost)
		if !nr.Exact {
			status = "bounded"
			costStr = "?"
		}
		fmt.Printf("  %-10s cost=%-6s [%g, %g] %s\n", n, costStr, nr.Lower, nr.Upper, status)
	}
	fmt.Println()

	for _, n := range res.Order {
		nr := res.Nodes[n]
		if !nr.Exact || len(nr.Proof) == 0 {
			continue
		}
		fmt.Printf("Proof for %s (cost %g):\n", n, nr.Cost)
		uses := map[string]int{}
		for _, ei := range nr.Proof {
			for _, pre := range res.Edges[ei].Premises {
				uses[pre]++
			}
		}
		for step, ei := range nr.Proof {
			e := res.Edges[ei]
			shared := ""
			for _, pre := range e.Premises {
				if uses[pre] > 1 {
					shared = "   ; shares " + pre
				}
			}
			lhs := strings.Join(e.Premises, ", ")
			fmt.Printf("  %2d. %-6s %s |- %s   (cost %g)%s\n", step+1, e.ID, lhs, e.Target, e.Cost, shared)
		}
		fmt.Println()
	}
}
