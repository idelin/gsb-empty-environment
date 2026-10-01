// Command schedule solves the task-scheduling Max-CSP instance and prints
// the globally optimal timetable found by branch and bound.
package main

import (
	"fmt"

	"maxcsp"
)

func main() {
	p := maxcsp.NewSchedulingProblem()

	fmt.Println("=== Task Scheduling: Weighted Max-CSP ===")
	fmt.Println("Variables: T1..T5, each takes a slot in {1..5}")
	fmt.Println("Hard constraints (must hold):")
	fmt.Println("  H1: no two tasks share a slot")
	fmt.Println("  H2: slot(T1) < slot(T3)")
	fmt.Println("  H3: slot(T5) <= 2 (deadline)")
	fmt.Println("Soft constraints (penalty weights):")
	fmt.Println("  S1 w=10: slot(T2) == 1")
	fmt.Println("  S2 w=8:  slot(T4) == slot(T1)+1")
	fmt.Println("  S3 w=4:  slot(T3) is even")
	fmt.Println("  S4 w=3:  slot(T4) <= 3")
	fmt.Println("  S5 w=2:  slot(T2) < slot(T4)")
	fmt.Println()

	res := maxcsp.Solve(p)
	if res.Assignment == nil {
		fmt.Println("No feasible schedule: hard constraints conflict.")
		return
	}

	fmt.Println("=== Optimal Schedule (branch and bound) ===")
	fmt.Println("Timetable:", maxcsp.Describe(res.Assignment))
	for _, task := range []string{"T1", "T2", "T3", "T4", "T5"} {
		fmt.Printf("  %s -> slot %d\n", task, res.Assignment[task])
	}
	fmt.Printf("Total penalty: %d\n", res.Cost)
	fmt.Printf("Violated soft constraints: %v\n", res.Violated)
	fmt.Printf("Search stats: %d nodes expanded, %d pruned\n", res.Nodes, res.Pruned)

	// Cross-check with exhaustive enumeration.
	bestCost, _ := maxcsp.BruteForce(p)
	fmt.Printf("Brute-force optimum: %d (matches: %v)\n", bestCost, bestCost == res.Cost)
}
