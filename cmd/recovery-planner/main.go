// Command recovery-planner reads a recovery plan description (JSON) and
// computes an execution order that minimizes peak disk usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"recovery-planner/planner"
)

func main() {
	input := flag.String("input", "", "input JSON file (default: stdin)")
	timeoutMs := flag.Int64("timeout", 0, "search timeout in milliseconds (overrides input limits; 0 = none)")
	maxExpanded := flag.Int64("max-expanded", 0, "maximum search nodes to expand (overrides input limits; 0 = none)")
	trace := flag.Bool("trace", false, "print the disk-usage trace of the returned plan to stderr")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *input != "" {
		f, err := os.Open(*input)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var plan planner.Plan
	if err := json.NewDecoder(r).Decode(&plan); err != nil {
		fatal(fmt.Errorf("decoding input: %w", err))
	}
	prob, err := planner.BuildProblem(plan)
	if err != nil {
		fatal(fmt.Errorf("invalid plan: %w", err))
	}
	lim := plan.Limits
	if *timeoutMs > 0 {
		lim.TimeoutMs = *timeoutMs
	}
	if *maxExpanded > 0 {
		lim.MaxExpanded = *maxExpanded
	}
	res := prob.Solve(lim)
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))

	if *trace && len(res.Order) > 0 {
		order := make([]int, len(res.Order))
		for i, id := range res.Order {
			idx, _ := prob.IndexOf(id)
			order[i] = idx
		}
		points, _, err := prob.Trace(order)
		if err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "%-16s %-28s %8s %8s\n", "block", "event", "used", "peak")
		for _, tp := range points {
			fmt.Fprintf(os.Stderr, "%-16s %-28s %8d %8d\n", tp.Block, tp.Event, tp.Used, tp.Peak)
		}
	}
	if res.Status == planner.StatusInfeasible {
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
