package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

func main() {
	inputPath := flag.String("input", "", "path to input JSON file (default: read from stdin)")
	asJSON := flag.Bool("json", false, "emit machine-readable JSON instead of a text report")
	flag.Parse()

	var data []byte
	var err error
	if *inputPath == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*inputPath)
	}
	if err != nil {
		fatal("reading input: %v", err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("parsing input JSON: %v", err)
	}
	solver, err := NewSolver(in)
	if err != nil {
		fatal("invalid instance: %v", err)
	}
	res := solver.Solve()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fatal("encoding result: %v", err)
		}
		return
	}
	printReport(os.Stdout, solver, res)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func printReport(w io.Writer, s *Solver, r Result) {
	fmt.Fprintf(w, "Optimal selection (%d of %d projects):\n", len(r.Selected), s.n)
	if len(r.Selected) == 0 {
		fmt.Fprintln(w, "  (none — empty selection is optimal)")
	}
	sel := make(map[string]bool, len(r.Selected))
	for _, name := range r.Selected {
		sel[name] = true
	}
	for i, name := range s.names {
		if !sel[name] {
			continue
		}
		fmt.Fprintf(w, "  %-16s cost %8d   standalone value %8d\n", name, s.cost[i], s.value[i])
	}

	fmt.Fprintln(w, "\nCost / benefit decomposition:")
	fmt.Fprintf(w, "  Total cost:            %10d  (budget %d, slack %d)\n", r.TotalCost, r.Budget, r.Budget-r.TotalCost)
	fmt.Fprintf(w, "  Standalone value sum:  %10d\n", r.StandaloneSum)
	if len(r.ActiveSynergies) == 0 {
		fmt.Fprintln(w, "  Active synergies:      none")
	} else {
		fmt.Fprintln(w, "  Active synergies (pairwise, kept separate from standalone values):")
		pairs := append([]SynergyPair(nil), r.ActiveSynergies...)
		sort.Slice(pairs, func(a, b int) bool {
			if pairs[a].A != pairs[b].A {
				return pairs[a].A < pairs[b].A
			}
			return pairs[a].B < pairs[b].B
		})
		for _, p := range pairs {
			fmt.Fprintf(w, "    %-14s %+10d\n", p.A+" + "+p.B+":", p.Value)
		}
	}
	fmt.Fprintf(w, "  Synergy sum:           %10d\n", r.SynergySum)
	fmt.Fprintf(w, "  Total benefit:         %10d\n", r.Total)

	fmt.Fprintln(w, "\nUpper-bound evidence (optimality certificate):")
	fmt.Fprintf(w, "  Relaxation at root:    %10d  (fractional-knapsack bound on pairwise upper marginals)\n", r.RootBound)
	fmt.Fprintf(w, "  Best solution value:   %10d\n", r.Total)
	fmt.Fprintf(w, "  Search nodes:          %10d  (pruned by bound: %d)\n", r.Nodes, r.Pruned)
	fmt.Fprintln(w, "  Every pruned subtree had a relaxation upper bound <= best; all other")
	fmt.Fprintln(w, "  nodes were explored exhaustively. Hence no feasible combination of the")
	fmt.Fprintf(w, "  %d projects can exceed %d.\n", s.n, r.Total)

	// Independent recomputation as a sanity check.
	if got := recomputeTotal(s, r.Selected); got != r.Total {
		fmt.Fprintf(w, "\nWARNING: verification mismatch: recomputed %d != reported %d\n", got, r.Total)
	} else {
		fmt.Fprintln(w, "  Verification: decomposition recomputed from selection matches total.")
	}
}

// recomputeTotal independently re-evaluates the objective for the selected
// names, keeping pairwise synergy terms pairwise.
func recomputeTotal(s *Solver, selected []string) int64 {
	mask := uint64(0)
	for _, name := range selected {
		for i, nm := range s.names {
			if nm == name {
				mask |= 1 << uint(i)
			}
		}
	}
	total := int64(0)
	for i := 0; i < s.n; i++ {
		if mask>>uint(i)&1 == 1 {
			total += s.value[i]
		}
	}
	for i := 0; i < s.n; i++ {
		if mask>>uint(i)&1 == 0 {
			continue
		}
		for j := i + 1; j < s.n; j++ {
			if mask>>uint(j)&1 == 1 {
				total += s.syn[i*s.n+j]
			}
		}
	}
	return total
}
