package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func usage() {
	fmt.Fprintf(os.Stderr, `feedback vertex set solver (minimum cost, then fewest vertices, then ID order)

usage:
  fvs solve  [-budget 500ms] graph.json   branch-and-bound solve
  fvs brute  graph.json                   exhaustive subset enumeration (n <= 25)
  fvs verify graph.json solution.json     check a deletion set + topological order

graph.json:     {"vertices":[{"id":"a","cost":2},...],"edges":[["a","b"],...]}
                cost defaults to 1 and must be non-negative; self-loops allowed
solution.json:  the JSON emitted by solve/brute
`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}

func loadGraph(path string) (*Graph, error) {
	var data []byte
	var err error
	if path == "" || path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("parsing graph JSON: %v", err)
	}
	return BuildGraph(&in)
}

func outputFromSolution(g *Graph, sol *Solution) *Output {
	topo, _ := g.TopoOrder(sol.Deleted)
	return &Output{
		Status:     "optimal",
		Deleted:    sortedIDs(g, sol.Deleted),
		Cost:       sol.Cost,
		Count:      sol.Count,
		TopoOrder:  topo,
		LowerBound: sol.Cost,
		UpperBound: sol.Cost,
	}
}

func cmdSolve(args []string, brute bool) {
	fs := flag.NewFlagSet("solve", flag.ExitOnError)
	budget := fs.Duration("budget", 0, "time budget (e.g. 250ms, 2s); 0 means no limit")
	fs.Parse(args)
	g, err := loadGraph(fs.Arg(0))
	if err != nil {
		fatal(err)
	}
	var out *Output
	if brute {
		sol, err := BruteForce(g)
		if err != nil {
			fatal(err)
		}
		out = outputFromSolution(g, sol)
	} else {
		out = Solve(g, time.Duration(*budget))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
}

func cmdVerify(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	g, err := loadGraph(fs.Arg(0))
	if err != nil {
		fatal(err)
	}
	data, err := os.ReadFile(fs.Arg(1))
	if err != nil {
		fatal(err)
	}
	var sol struct {
		Deleted   []string `json:"deleted"`
		TopoOrder []string `json:"topological_order"`
	}
	if err := json.Unmarshal(data, &sol); err != nil {
		fatal(fmt.Errorf("parsing solution JSON: %v", err))
	}
	if err := VerifyCertificate(g, sol.Deleted, sol.TopoOrder); err != nil {
		fmt.Println("INVALID:", err)
		os.Exit(1)
	}
	fmt.Println("OK: deletion set is a feedback vertex set and the topological order certifies acyclicity")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "solve":
		cmdSolve(os.Args[2:], false)
	case "brute":
		cmdSolve(os.Args[2:], true)
	case "verify":
		cmdVerify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}
