// Command updater reads a problem description (JSON) and prints the optimal
// selection. With -verify it cross-checks the backtracking solver against
// full enumeration.
//
// Input schema:
//
//	{
//	  "locked":     {"app": "1.0.0"},
//	  "candidates": {"app": ["1.0.0", "2.0.0"], "lib": ["1.0.0"]},
//	  "banned":     {"app": ["1.0.0"]},
//	  "deps":       {"app": {"2.0.0": [{"pkg": "lib", "min": "1.0.0"}]}}
//	}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"updater"
)

func main() {
	verify := flag.Bool("verify", false, "cross-check Solve against full enumeration")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: updater [-verify] problem.json")
		os.Exit(2)
	}

	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var p updater.Problem
	if err := json.Unmarshal(data, &p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	sol := updater.Solve(&p)
	if sol == nil {
		fmt.Fprintln(os.Stderr, "no feasible solution")
		os.Exit(1)
	}
	if *verify {
		ref := updater.BruteForce(&p)
		if ref == nil || ref.Changes != sol.Changes || ref.Total != sol.Total {
			fmt.Fprintln(os.Stderr, "verification failed: solver disagrees with enumeration")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "verified against full enumeration")
	}

	out := struct {
		Selected map[string]updater.Version `json:"selected"`
		Changes  int                        `json:"changes"`
		Total    int                        `json:"total"`
	}{sol.Selected, sol.Changes, sol.Total}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
