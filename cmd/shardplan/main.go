// Command shardplan reads a planning request from stdin as JSON and writes
// the resulting plan to stdout as JSON.
//
// Input:
//
//	{"tests": [{"name": "t1", "duration": 2.5, "group": "g", "previous": 0}],
//	 "executors": 3, "capacity": 10}
//
// "group" and "previous" are optional; "previous" defaults to 0, so pass -1
// for tests without a previous executor.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"shardplan"
)

type request struct {
	Tests     []shardplan.Test `json:"tests"`
	Executors int              `json:"executors"`
	Capacity  float64          `json:"capacity"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, "shardplan: invalid input:", err)
		os.Exit(2)
	}
	res, err := shardplan.Plan(req.Tests, req.Executors, req.Capacity)
	if err != nil {
		fmt.Fprintln(os.Stderr, "shardplan:", err)
		os.Exit(2)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "shardplan:", err)
		os.Exit(2)
	}
	if !res.Feasible {
		fmt.Fprintf(os.Stderr, "shardplan: infeasible within capacity; need at least %v\n", res.LowerBound)
		os.Exit(1)
	}
}
