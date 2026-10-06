// Command diagdemo runs an end-to-end example: SCC compression and minimum
// root-set cover with evidence, followed by optimal decision-tree search
// and per-path replay verification.
package main

import (
	"fmt"

	"github.com/example/diag"
)

func main() {
	// Part 1: fault-dependency graph with a cycle {db, cache, queue}.
	g := diag.NewGraph()
	for _, e := range [][2]string{
		{"db", "cache"}, {"cache", "queue"}, {"queue", "db"}, // cycle
		{"db", "api"}, {"api", "web"},
		{"net", "web"},
		{"net", "worker"},
	} {
		g.AddEdge(e[0], e[1])
	}
	observed := []string{"api", "web", "worker"}

	res, err := diag.MinRootSets(g, observed, 0)
	if err != nil {
		panic(err)
	}
	fmt.Println("== SCC compression ==")
	for i, c := range res.Components {
		fmt.Printf("  comp %d: %v\n", i, c)
	}
	fmt.Printf("candidates (source SCCs): %v\n", res.Candidates)
	fmt.Printf("minimum root set size: %d (%d solution(s))\n", res.MinSize, res.NumSolutions)
	for i, sol := range res.Solutions {
		fmt.Printf("solution %d: %v\n", i, sol.Roots)
		for _, ev := range sol.Evidence {
			for _, w := range ev.Faults {
				fmt.Printf("    root %v covers %s via %v\n", ev.Root, w.Fault, w.Path)
			}
		}
	}

	viol, err := diag.VerifyCyclesSingleRoot(g, observed)
	if err != nil {
		panic(err)
	}
	fmt.Printf("cycle single-root check: %d violation(s)\n\n", len(viol))

	// Part 2: worlds are plausible root sets; tests spend a shared budget.
	worlds := []string{"{db-cycle}", "{net}", "{db-cycle,net}"}
	tests := []diag.Test{
		{Name: "ping-worker", Cost: 2, Outcome: []string{"timeout", "timeout", "ok"}},
		{Name: "db-probe", Cost: 3, Outcome: []string{"slow", "ok", "slow"}},
		{Name: "full-trace", Cost: 9, Outcome: []string{"cycle-only", "net-only", "both"}},
	}
	strat, err := diag.OptimalStrategy(worlds, tests)
	if err != nil {
		panic(err)
	}
	fmt.Println("== optimal strategy ==")
	fmt.Print(strat.Format())
	fmt.Printf("worst-case budget: %d\n\n", strat.WorstCost)

	paths, worst, err := diag.ReplayStrategy(strat, tests)
	if err != nil {
		panic(err)
	}
	fmt.Println("== replay verification ==")
	for _, p := range paths {
		fmt.Printf("  world %-16s cost %d -> %s (ok=%v) steps:", p.World, p.TotalCost, p.Identified, p.OK)
		for _, s := range p.Steps {
			fmt.Printf(" %s=%s", s.Test, s.Outcome)
		}
		fmt.Println()
	}
	fmt.Printf("replay worst cost: %d (matches strategy: %v)\n", worst, worst == strat.WorstCost)
}
