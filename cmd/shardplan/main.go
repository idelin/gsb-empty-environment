// Command shardplan reads a planning request as JSON from stdin and writes
// the resulting plan as JSON to stdout.
//
// Input:
//
//	{
//	  "executors": 3,
//	  "loadLimit": 60,
//	  "tests": [
//	    {"name": "TestA", "duration": 12, "executor": 0},
//	    {"name": "TestB", "duration": 8,  "executor": 1, "group": "db"}
//	  ]
//	}
//
// "executor" is the test's current executor; omit it (or use -1) for new
// tests. "group" forces tests onto the same executor.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"shardplan"
)

type request struct {
	Executors int              `json:"executors"`
	LoadLimit int64            `json:"loadLimit"`
	Tests     []shardplan.Test `json:"tests"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "shardplan: invalid input: %v\n", err)
		os.Exit(2)
	}
	res, err := shardplan.Plan(req.Tests, req.Executors, req.LoadLimit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shardplan: %v\n", err)
		os.Exit(2)
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "shardplan: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))
	if !res.Feasible {
		os.Exit(1)
	}
}
