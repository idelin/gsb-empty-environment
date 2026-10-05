// Command mealsolver reads a JSON problem definition and prints either a
// minimum-cost 5-day meal plan or, when infeasible, a minimal conflicting
// constraint subset.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"mealsolver/solver"
)

func main() {
	configPath := flag.String("config", "examples/feasible.json", "path to the JSON problem definition")
	flag.Parse()

	data, err := os.ReadFile(*configPath)
	if err != nil {
		fatal(err)
	}
	var cfg solver.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fatal(fmt.Errorf("parse %s: %w", *configPath, err))
	}

	plan, err := solver.Solve(cfg)
	if err == solver.ErrInfeasible {
		fmt.Println("无可行解：存在相互冲突的约束。正在计算冲突约束子集……")
		subset, ierr := solver.FindConflictSubset(cfg)
		if ierr != nil {
			fatal(ierr)
		}
		fmt.Printf("\n最小冲突约束子集（共 %d 条，删除其中任意一条即可恢复可行）：\n", len(subset))
		for i, c := range subset {
			fmt.Printf("  %d. %s\n", i+1, c)
		}
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}

	names := make([]string, 0, len(cfg.Nutrients))
	for name := range cfg.Nutrients {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("最优总成本: %.2f\n", plan.TotalCost)
	for i, day := range plan.Days {
		fmt.Printf("\n第 %d 天（成本 %.2f）\n", i+1, day.Cost)
		for _, s := range day.Servings {
			fmt.Printf("  - %s x%d 份（档位 %.2f，成本 %.2f）\n", s.Dish, s.Count, s.Tier, s.Cost)
		}
		for _, name := range names {
			r := cfg.Nutrients[name]
			fmt.Printf("    %s: %.1f  （每日要求 [%v, %v]）\n", name, day.Totals[name], r.Min, r.Max)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
