// Command demo runs Régin's AllDifferent propagator on two scenarios:
// the acceptance boundary (4 variables over 3 values, infeasible before
// search) and a filtering example where SCC analysis prunes edges.
package main

import (
	"fmt"
	"sort"

	"alldiff"
)

func printScenario(title string, vars []alldiff.Variable) {
	fmt.Printf("=== %s ===\n", title)
	names := make([]string, 0, len(vars))
	domains := make(map[string][]int, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
		domains[v.Name] = v.Domain
		fmt.Printf("  domain(%s) = %v\n", v.Name, v.Domain)
	}

	res := alldiff.Propagate(vars)
	if !res.Feasible {
		fmt.Printf("  => INFEASIBLE (detected at matching stage, before any search)\n")
		fmt.Printf("     reason: %s\n\n", res.Reason)
		return
	}

	fmt.Printf("  => feasible; maximum matching of size %d:\n", len(res.Matching))
	for _, e := range res.Matching {
		fmt.Printf("     %s = %d\n", e.Variable, e.Value)
	}
	if len(res.Removed) == 0 {
		fmt.Println("  => SCC filtering removed no edges")
	} else {
		fmt.Printf("  => SCC filtering removed %d edge(s):\n", len(res.Removed))
		for _, e := range res.Removed {
			fmt.Printf("     %s != %d (in no feasible solution)\n", e.Variable, e.Value)
		}
	}
	sort.Strings(names)
	fmt.Println("  => pruned domains:")
	for _, name := range names {
		fmt.Printf("     domain(%s) = %v  (was %v)\n", name, res.Domains[name], domains[name])
	}
	fmt.Println()
}

func main() {
	printScenario("Acceptance boundary: 4 variables, 3 shared values", []alldiff.Variable{
		{Name: "X1", Domain: []int{1, 2, 3}},
		{Name: "X2", Domain: []int{1, 2, 3}},
		{Name: "X3", Domain: []int{1, 2, 3}},
		{Name: "X4", Domain: []int{1, 2, 3}},
	})

	printScenario("SCC filtering: Hall set {1,2} locked by X1,X2", []alldiff.Variable{
		{Name: "X1", Domain: []int{1, 2}},
		{Name: "X2", Domain: []int{1, 2}},
		{Name: "X3", Domain: []int{1, 2, 3, 4}},
		{Name: "X4", Domain: []int{3, 4, 5}},
	})
}
