// Command sccp-demo runs sparse conditional constant propagation over a
// set of example control-flow graphs, prints the transformation report,
// and verifies each optimized program against the original by running
// every possible 8-bit input combination.
package main

import (
	"fmt"

	"sccp"
)

func main() {
	for _, f := range sccp.Examples() {
		fmt.Printf("=== %s ===\n%s\n", f.Name, f)
		a := sccp.Analyze(f)
		opt, rep := sccp.Optimize(a)
		fmt.Print(rep)
		fmt.Printf("\noptimized:\n%s\n", opt)
		n, err := sccp.CheckExhaustive(f, opt, 100000)
		if err != nil {
			fmt.Printf("EXHAUSTIVE CHECK FAILED: %v\n\n", err)
			continue
		}
		fmt.Printf("exhaustive check: %d input combinations, all outcomes match\n\n", n)
	}
}
