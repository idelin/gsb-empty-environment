package main

import (
	"fmt"
	"os"

	"example.org/netsec/q0113/audit"
)

func main() {
	fmt.Println("reviewed implementation: legacy thread-token capture")

	shortest, ok := audit.ShortestViolation(16)
	if !ok {
		fmt.Fprintln(os.Stderr, "no stale/cross-request access found")
		os.Exit(1)
	}
	fmt.Printf("\nshortest violation (%d steps):\n%s", len(shortest.Steps), shortest)

	cross, ok := audit.CrossRequestViolation(16)
	if !ok {
		fmt.Fprintln(os.Stderr, "no cross-request identity access found")
		os.Exit(1)
	}
	fmt.Printf("\nshortest cross-request violation (%d steps):\n%s", len(cross.Steps), cross)

	covered, ok := audit.CoverageViolation(16)
	if !ok {
		fmt.Fprintln(os.Stderr, "no coverage trace found")
		os.Exit(1)
	}
	fmt.Printf("\ncoverage violation (%d steps):\n%s", len(covered.Steps), covered)
}
