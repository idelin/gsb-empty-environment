package main

import (
	"fmt"
	"strings"

	"cteopt"
)

func main() {
	tables := cteopt.DemoTables()

	fmt.Println("=== Query ===")
	fmt.Println(cteopt.DemoSQL)

	root, cte := cteopt.BuildDemoPlan()
	cteopt.Annotate(root, tables)
	fmt.Println("\n=== Plan before optimization (CTE inlined per reference) ===")
	fmt.Print(cteopt.Explain(root))

	opt := &cteopt.Optimizer{Tables: tables}
	optimized := opt.Optimize(root)

	fmt.Println("=== Optimizer decisions ===")
	for _, d := range opt.Decisions {
		verdict := "INLINE"
		if d.Materialized {
			verdict = "MATERIALIZE"
		}
		fmt.Printf("subexpr %-28q refs=%d cost=%.0f inline=%.0f materialize=%.0f -> %s\n",
			d.Label, d.Refs, d.SubexprCost, d.InlineCost, d.MaterializeCost, verdict)
	}

	fmt.Println("\n=== Plan after optimization ===")
	fmt.Print(cteopt.Explain(optimized))

	inlineExec := cteopt.NewExecutor(tables)
	inlineRows := inlineExec.Collect(root)
	matExec := cteopt.NewExecutor(tables)
	matRows := matExec.Collect(optimized)

	fmt.Println("=== Execution ===")
	fmt.Printf("inlined:      CTE subtree opened %d times, CTE rows recomputed %d times, result rows=%d\n",
		inlineExec.Stats.Opens[cte], inlineExec.Stats.RowsProduced[cte], len(inlineRows))
	fmt.Printf("materialized: CTE subtree opened %d times, CTE rows computed %d times, result rows=%d\n",
		matExec.Stats.Opens[cte], matExec.Stats.RowsProduced[cte], len(matRows))
	fmt.Printf("results identical: %v\n", fmt.Sprint(sorted(inlineRows)) == fmt.Sprint(sorted(matRows)))
	fmt.Printf("result: %v\n", sorted(matRows))

	fmt.Println("\n=== Cheap CTE: optimizer keeps it inlined ===")
	cheapRoot, _ := cteopt.BuildCheapCTEPlan()
	cheapOpt := &cteopt.Optimizer{Tables: cteopt.CheapTables()}
	cheapOptimized := cheapOpt.Optimize(cheapRoot)
	for _, d := range cheapOpt.Decisions {
		verdict := "INLINE"
		if d.Materialized {
			verdict = "MATERIALIZE"
		}
		fmt.Printf("subexpr %-28q refs=%d cost=%.1f inline=%.1f materialize=%.1f -> %s\n",
			d.Label, d.Refs, d.SubexprCost, d.InlineCost, d.MaterializeCost, verdict)
	}
	fmt.Print(cteopt.Explain(cheapOptimized))
}

func sorted(rows []cteopt.Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprint(r))
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if strings.Compare(out[j], out[i]) < 0 {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
