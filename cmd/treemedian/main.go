// Command treemedian reads a facility-location instance from stdin and
// prints the placement, per-node assignment and total cost as JSON.
//
// Input format (all integers, node ids are 0-based):
//
//	n K
//	d_0 d_1 ... d_{n-1}        node demands
//	f f_1 ... f_f              number of forbidden nodes, then their ids
//	u v w                      n-1 edge lines
//
// Output JSON:
//
//	{"status":"exact|upper_bound|infeasible","cost":C,
//	 "facilities":[...],"assignment":[facility per node, -1 if none]}
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"treemedian"
)

type output struct {
	Status     string `json:"status"`
	Cost       int64  `json:"cost"`
	Facilities []int  `json:"facilities"`
	Assignment []int  `json:"assignment"`
}

func main() {
	budget := flag.Int64("budget", treemedian.DefaultMaxOps, "operation budget for the exact DP; on exhaustion a greedy upper bound is returned")
	flag.Parse()

	in := bufio.NewReader(os.Stdin)
	var n, k int
	if _, err := fmt.Fscan(in, &n, &k); err != nil {
		fail("reading n K: %v", err)
	}
	s := treemedian.NewSolver(n, k)
	s.MaxOps = *budget
	for i := 0; i < n; i++ {
		if _, err := fmt.Fscan(in, &s.Demand[i]); err != nil {
			fail("reading demand %d: %v", i, err)
		}
	}
	var f int
	if _, err := fmt.Fscan(in, &f); err != nil {
		fail("reading forbidden count: %v", err)
	}
	for i := 0; i < f; i++ {
		var x int
		if _, err := fmt.Fscan(in, &x); err != nil {
			fail("reading forbidden node: %v", err)
		}
		if x < 0 || x >= n {
			fail("forbidden node %d out of range", x)
		}
		s.Forbidden[x] = true
	}
	for i := 0; i < n-1; i++ {
		var u, v int
		var w int64
		if _, err := fmt.Fscan(in, &u, &v, &w); err != nil {
			fail("reading edge %d: %v", i, err)
		}
		if u < 0 || u >= n || v < 0 || v >= n {
			fail("edge %d-%d out of range", u, v)
		}
		if w < 0 {
			fail("edge %d-%d has negative length %d", u, v, w)
		}
		s.AddEdge(u, v, w)
	}

	res := s.Solve()
	out := output{
		Cost:       res.Cost,
		Facilities: res.Facilities,
		Assignment: res.Assignment,
	}
	switch {
	case !res.Feasible:
		out.Status = "infeasible"
	case res.Exact:
		out.Status = "exact"
	default:
		out.Status = "upper_bound"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fail("encoding result: %v", err)
	}
	if !res.Feasible {
		os.Exit(2)
	}
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "treemedian: "+format+"\n", args...)
	os.Exit(1)
}
