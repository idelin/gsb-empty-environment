// Command recover-plan reads a recovery spec (JSON) and prints an execution
// order that minimizes peak disk usage, together with certified bounds.
//
//	recover-plan [-spec file.json] [-budget 5s] [-quota BYTES] [-enum 14]
//
// Input spec:
//
//	{
//	  "blocks": [
//	    {"id": "a", "compressed": 10, "temp": 24, "final": 8, "deps": []},
//	    {"id": "b", "compressed": 6,  "temp": 12, "final": 4, "deps": ["a"]}
//	  ],
//	  "outputs": ["b"],        // optional, defaults to DAG sinks
//	  "quota": 40              // optional, 0 = no quota
//	}
//
// The output always contains a proven-feasible upper bound (the peak of the
// returned order) and a legal lower bound, so interrupting the search via
// -budget still yields a certified gap.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"recoverplanner/planner"
)

func main() {
	specPath := flag.String("spec", "-", "spec JSON file, or - for stdin")
	budget := flag.Duration("budget", 5*time.Second, "search time budget (0 = unlimited)")
	quota := flag.Int64("quota", -1, "disk quota in bytes, overrides spec (-1 = use spec)")
	enumBelow := flag.Int("enum", 12, "enumerate topological orders exactly up to this many blocks")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *specPath != "-" {
		f, err := os.Open(*specPath)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var spec planner.Spec
	if err := json.NewDecoder(r).Decode(&spec); err != nil {
		fatal(fmt.Errorf("decoding spec: %w", err))
	}
	p, err := planner.New(spec)
	if err != nil {
		fatal(fmt.Errorf("invalid spec: %w", err))
	}

	q := spec.Quota
	if *quota >= 0 {
		q = *quota
	}
	opts := planner.Options{EnumerateBelow: *enumBelow}
	if *budget > 0 {
		opts.Deadline = time.Now().Add(*budget)
	}
	res := planner.Solve(p, q, opts)

	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
	if !res.Feasible {
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "recover-plan:", err)
	os.Exit(1)
}
