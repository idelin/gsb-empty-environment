package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	var (
		inputPath = flag.String("input", "", "input JSON file (default: stdin)")
		maxNodes  = flag.Int64("max-nodes", 0, "branch-and-bound node budget (0 = unlimited)")
		dpBudget  = flag.Int("dp-budget", 22, "max targets solved by subset DP")
		verify    = flag.Bool("verify", false, "cross-check with brute force over all candidate subsets (small grids)")
		pretty    = flag.Bool("pretty", true, "pretty-print JSON output")
	)
	flag.Parse()

	var data []byte
	var err error
	if *inputPath == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*inputPath)
	}
	if err != nil {
		fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(fmt.Errorf("parse input: %w", err))
	}

	opt := Options{DPBudget: *dpBudget, MaxNodes: *maxNodes}
	res := Solve(in, opt)

	if *verify && res.Feasible && !res.Interrupted {
		best, err := BruteForce(in, opt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify: %v\n", err)
		} else if best.Area != res.TotalArea || best.Count != res.RectCount {
			fmt.Fprintf(os.Stderr, "verify: MISMATCH solver=(%d,%d) brute=(%d,%d)\n",
				res.TotalArea, res.RectCount, best.Area, best.Count)
			os.Exit(2)
		} else {
			fmt.Fprintf(os.Stderr, "verify: ok (area=%d count=%d)\n", best.Area, best.Count)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(res); err != nil {
		fatal(err)
	}
	if !res.Feasible {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
