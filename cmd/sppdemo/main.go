// Command sppdemo runs the BGP Stable Paths Problem toolkit against the
// canonical acceptance gadgets and prints the stability verdict, dispute
// wheels, convergence certificate and minimum repair for each scenario.
package main

import (
	"fmt"
	"os"
	"strings"

	"prescreen.local/ps-f69a1f50e0/spp"
)

func main() {
	scenarios := []struct {
		name string
		fn   func() *spp.Instance
	}{
		{"bad-gadget", badGadget},
		{"disagree-4as", disagree4},
		{"valley-free", valleyFreeClean},
		{"backup-dispute", valleyFreeBackup},
	}
	selected := scenarios
	if len(os.Args) > 1 {
		selected = nil
		for _, sc := range scenarios {
			if sc.name == os.Args[1] {
				selected = append(selected, sc)
			}
		}
		if selected == nil {
			fmt.Fprintf(os.Stderr, "unknown scenario %q; choose one of: %s\n",
				os.Args[1], strings.Join(names(scenarios), ", "))
			os.Exit(2)
		}
	}
	for _, sc := range selected {
		runScenario(sc.name, sc.fn())
	}
}

func names(sc []struct {
	name string
	fn   func() *spp.Instance
}) []string {
	out := make([]string, len(sc))
	for i, s := range sc {
		out[i] = s.name
	}
	return out
}

func runScenario(name string, inst *spp.Instance) {
	fmt.Printf("=== scenario %s ===\n", name)
	fmt.Printf("validation: clean=%v valleyFree=%v commercialCycleFree=%v exportConsistent=%v\n",
		inst.Report.IsClean(), inst.Report.ValleyFree,
		inst.Report.CommercialCycleFree, inst.Report.ExportConsistent)
	for _, v := range inst.Report.Violations {
		fmt.Printf("  violation: %s\n", v)
	}
	analysis := spp.Analyze(inst, spp.SolveOptions{}, 8)
	res := analysis.Solve
	fmt.Printf("outcome: %s (%d stable assignment(s))\n", res.Outcome, len(res.Solutions))
	for i, sol := range res.Solutions {
		fmt.Printf("  SPA[%d]: %s\n", i, formatAssignment(inst, sol))
	}
	if len(analysis.Wheels) > 0 {
		fmt.Printf("dispute wheels: %d\n", len(analysis.Wheels))
		printWheel(analysis.Wheels[0])
	}
	if analysis.ConvergenceProof != nil {
		fmt.Printf("convergence lock-in order: %v\n", analysis.ConvergenceProof.LockInOrder)
	}
	if res.Outcome != spp.OutcomeUnique {
		plans, err := inst.MinimumRepair(spp.RepairOptions{AllowEdgeDisable: true})
		if err != nil {
			fmt.Printf("minimum repair: %v\n", err)
		} else {
			fmt.Printf("minimum repair size: %d action(s); %d optimal plan(s)\n",
				len(plans[0].Actions), len(plans))
			for _, a := range plans[0].Actions {
				fmt.Printf("  action: %s\n", a.Describe())
			}
			fmt.Printf("repaired outcome: %s, wheels left: %d, reachability kept: %v\n",
				plans[0].Verify.Outcome, len(plans[0].RemainingWheels), plans[0].ReachabilityKept)
		}
	}
	fmt.Println()
}

func printWheel(w spp.DisputeWheel) {
	fmt.Printf("  pivots: %v\n", w.Pivots)
	for i, u := range w.Pivots {
		fmt.Printf("  u_%d=%d  Q_i=%v  R_i=%v  (%s: rank %d < %d)\n",
			i, u, w.Spokes[i], w.Rims[i], w.Proof[i].Relation,
			w.Proof[i].BetterRank, w.Proof[i].WorseRank)
	}
}

func formatAssignment(inst *spp.Instance, a spp.Assignment) string {
	var b strings.Builder
	first := true
	for _, u := range inst.SortedNodes() {
		if !first {
			b.WriteString(", ")
		}
		first = false
		fmt.Fprintf(&b, "%d->%v", u, a[u])
	}
	return b.String()
}

func badGadget() *spp.Instance {
	g, _ := spp.NewASGraph([]int{0, 1, 2, 3}, []spp.Link{
		{A: 0, B: 1}, {A: 0, B: 2}, {A: 0, B: 3},
		{A: 1, B: 2}, {A: 2, B: 3}, {A: 3, B: 1},
	})
	inst, err := spp.Build(g, spp.PolicyConfig{
		Origin:       0,
		ExplicitOnly: true,
		ExtraPaths: map[int][][]int{
			1: {{1, 3, 0}, {1, 0}},
			2: {{2, 1, 0}, {2, 0}},
			3: {{3, 2, 0}, {3, 0}},
		},
	})
	must(err)
	return inst
}

func disagree4() *spp.Instance {
	g, _ := spp.NewASGraph([]int{0, 1, 2, 3}, []spp.Link{
		{A: 0, B: 1}, {A: 0, B: 2}, {A: 1, B: 2}, {A: 3, B: 1},
	})
	inst, err := spp.Build(g, spp.PolicyConfig{
		Origin:       0,
		ExplicitOnly: true,
		ExtraPaths: map[int][][]int{
			1: {{1, 2, 0}, {1, 0}},
			2: {{2, 1, 0}, {2, 0}},
			3: {{3, 1, 0}},
		},
	})
	must(err)
	return inst
}

func valleyFreeClean() *spp.Instance {
	g, _ := spp.NewASGraph([]int{1, 2, 3, 4}, []spp.Link{
		{A: 1, B: 4, Relation: spp.RelationCustomerToProvider},
		{A: 2, B: 4, Relation: spp.RelationCustomerToProvider},
		{A: 3, B: 1, Relation: spp.RelationCustomerToProvider},
	})
	inst, err := spp.Build(g, spp.PolicyConfig{Origin: 4, StrictGR: true})
	must(err)
	return inst
}

func valleyFreeBackup() *spp.Instance {
	g, _ := spp.NewASGraph([]int{1, 2, 3, 4}, []spp.Link{
		{A: 1, B: 4, Relation: spp.RelationCustomerToProvider},
		{A: 2, B: 4, Relation: spp.RelationCustomerToProvider},
		{A: 3, B: 1, Relation: spp.RelationCustomerToProvider},
		{A: 1, B: 2, Relation: spp.RelationPeer},
		{A: 3, B: 2, Relation: spp.RelationCustomerToProvider},
	})
	inst, err := spp.Build(g, spp.PolicyConfig{
		Origin: 4,
		TierFor: map[int]map[string]spp.LocalPrefTier{
			3: {"[3 2 4]": spp.PrefCustomer},
			2: {"[2 1 4]": spp.PrefCustomer},
		},
	})
	must(err)
	return inst
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
