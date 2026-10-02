// Demo of the incremental formula dependency graph engine.
//
// Scenario (acceptance criteria):
//   - build A1->B1->C1 and D1->C1
//   - change A1: only B1 and C1 are recomputed, D1 stays untouched
//   - adding C1->A1 is rejected with the full cycle path
package main

import (
	"fmt"
	"os"

	engine "sheetcalc"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	e := engine.New()

	fmt.Println("== build graph: A1->B1->C1, D1->C1 (plus unrelated E1=D1*3) ==")
	must(e.SetValue("A1", 1))
	must(e.SetValue("D1", 10))
	must(e.SetFormula("B1", "A1*2"))
	must(e.SetFormula("C1", "B1+D1"))
	must(e.SetFormula("E1", "D1*3"))
	printState(e, "A1", "B1", "C1", "D1", "E1")

	fmt.Println("\n== single-point change: A1 = 5 ==")
	must(e.SetValue("A1", 5))
	printState(e, "A1", "B1", "C1", "D1", "E1")
	fmt.Printf("eval counts  B1=%d C1=%d E1=%d (E1 untouched: not downstream of A1)\n",
		e.EvalCount("B1"), e.EvalCount("C1"), e.EvalCount("E1"))

	fmt.Println("\n== attempt to add C1->A1 (A1 = C1+1) ==")
	err := e.SetFormula("A1", "C1+1")
	if cerr, ok := err.(*engine.CycleError); ok {
		fmt.Println("rejected:", cerr)
	} else if err != nil {
		fmt.Println("rejected:", err)
	} else {
		fmt.Println("BUG: cycle was not detected")
		os.Exit(1)
	}
	fmt.Println("state after rejection (isolated, unchanged):")
	printState(e, "A1", "B1", "C1", "D1", "E1")
}

func printState(e *engine.Engine, names ...string) {
	for _, n := range names {
		v, err := e.Get(n)
		if err != nil {
			fmt.Printf("  %s: %v\n", n, err)
			continue
		}
		fmt.Printf("  %s = %g\n", n, v)
	}
}
