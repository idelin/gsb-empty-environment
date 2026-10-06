package main

import "fmt"

func main() {
	inst := demoInstance()

	fmt.Println("=== Part 1: cheapest expanded set (subset enumeration) ===")
	res := inst.SolvePart1()
	fmt.Printf("expanded groups: %v\n", inst.MaskNames(res.Mask))
	fmt.Printf("shared expansion cost: %d, extra-leaf penalty: %d, total: %d\n",
		res.ExpandCost, res.LeafPenalty, res.Cost)
	fmt.Printf("extra exposed leaves: %v\n", res.ExposedLeaves)
	fmt.Printf("feasible sets scanned: %d\n", res.FeasibleSets)

	req := inst.RequiredMask()
	re, rp, _ := inst.evalMask(req)
	fmt.Printf("structural check (ancestor closure of targets): mask=%v cost=%d (match=%v)\n\n",
		inst.MaskNames(req), re+rp, re+rp == res.Cost)

	fmt.Println("=== Part 2: expand/collapse plan under a memory budget ===")
	budget := 29
	plan := inst.SolvePart2(budget)
	if !plan.OK {
		fmt.Println("no feasible plan within budget")
		return
	}
	fmt.Printf("total action cost: %d, peak memory: %d (budget %d)\n",
		plan.TotalCost, plan.Peak, budget)
	fmt.Print(inst.Replay(plan, budget))
	if vc, ok := inst.VerifyPart2(budget); ok && vc == plan.TotalCost {
		fmt.Printf("state-search cross-check: optimum %d confirmed\n", vc)
	} else {
		fmt.Printf("state-search cross-check MISMATCH: verifier=%v(%v) plan=%d\n", vc, ok, plan.TotalCost)
	}
}
