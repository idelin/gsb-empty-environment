// Command grantsweep runs the grant differential search and prints the
// shortest unauthorized-success trace for each modeled weakness.
package main

import (
	"fmt"
	"os"

	"example.org/netsec/q0158/grantsweep"
)

func main() {
	cfg := grantsweep.DefaultConfig()

	fmt.Println("== Cache key differential (pending index) ==")
	for _, c := range grantsweep.SearchCacheKeyCollisions() {
		fmt.Printf("weak key %q collapses entries differing in a security dimension\n", c.Key)
	}

	fmt.Println()
	fmt.Println("== Create/Use/Retire differential BFS ==")
	findings, stats, err := grantsweep.SearchAllPolicies(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, p := range grantsweep.AllPolicies() {
		if p == (grantsweep.Policy{}) {
			continue
		}
		if p.NonAtomicRetire {
			continue
		}
		f := findings[p.Name()]
		if f == nil {
			fmt.Printf("%-20s no divergence (states=%d)\n", p.Name(), stats[p.Name()])
			continue
		}
		fmt.Print(grantsweep.RenderTrace(f))
	}

	fmt.Println()
	fmt.Println("== Concurrent retire/use scheduler ==")
	race, interleavings := grantsweep.SearchRetireRace(grantsweep.Policy{NonAtomicRetire: true})
	if race == nil {
		fmt.Println("no race found")
	} else {
		fmt.Printf("examined %d interleavings\n", interleavings)
		fmt.Print(grantsweep.RenderSchedule(race))
	}

	fmt.Println()
	fmt.Println("reference model: no unauthorized success")
}
