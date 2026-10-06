package main

import (
	"fmt"
	"strings"

	"diaglab"
)

func part1() {
	fmt.Println("========== 第一部分：故障依赖图的最小根集合 ==========")
	g := diaglab.NewGraph()
	// 循环依赖：A -> B -> C -> A（一个强连通分量）
	g.AddEdge("A", "B")
	g.AddEdge("B", "C")
	g.AddEdge("C", "A")
	g.AddEdge("C", "D")
	g.AddEdge("D", "E")
	g.AddEdge("F", "E")
	// 另一个循环：G <-> H
	g.AddEdge("G", "H")
	g.AddEdge("H", "G")

	observed := []string{"D", "E", "H"}
	candidates := []string{"A", "B", "F", "G"}

	fmt.Println("强连通分量压缩：")
	for i, comp := range g.SCCs() {
		fmt.Printf("  SCC%d = {%s}\n", i, strings.Join(comp, ","))
	}

	res, err := diaglab.MinRootCover(diaglab.CoverRequest{
		G: g, Observed: observed, Candidates: candidates,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("观测故障: {%s}  候选根: {%s}\n",
		strings.Join(observed, ","), strings.Join(candidates, ","))
	fmt.Printf("最少候选根集合（大小 %d）: {%s}\n", res.MinSize, strings.Join(res.Roots, ","))

	fmt.Println("覆盖证据（可达路径）：")
	for _, ev := range res.Evidence {
		for _, c := range ev.Covers {
			fmt.Printf("  根 %s 解释观测 %s，路径: %s\n", ev.Root, c.Observed, strings.Join(c.Path, " -> "))
		}
	}
	if len(res.Uncovered) > 0 {
		fmt.Printf("  未覆盖观测: {%s}\n", strings.Join(res.Uncovered, ","))
	} else {
		fmt.Println("  全部观测均被覆盖")
	}

	fmt.Println("子集穷举核对：")
	fmt.Printf("  穷举全部 2^%d 个候选子集，最小大小与分支定界一致: %v\n",
		len(candidates), res.VerifiedOptimal)
	fmt.Printf("  循环未被当作多个独立根: %v\n", res.CycleCheckPassed)
	for _, r := range res.CycleReports {
		fmt.Println("  " + r)
	}
	fmt.Println()
}

func part2() {
	fmt.Println("========== 第二部分：最小最坏费用决策树 ==========")
	worlds := []diaglab.World{
		{Name: "w0", RootSet: []string{"A"}},
		{Name: "w1", RootSet: []string{"B"}},
		{Name: "w2", RootSet: []string{"G"}},
		{Name: "w3", RootSet: []string{"A", "G"}},
		{Name: "w4", RootSet: []string{"B", "G"}},
		{Name: "w5", RootSet: []string{"F"}},
	}
	inSet := func(w int, root string) bool {
		for _, r := range worlds[w].RootSet {
			if r == root {
				return true
			}
		}
		return false
	}
	probe := func(root string) func(int) string {
		return func(w int) string {
			if inSet(w, root) {
				return root + "+"
			}
			return root + "-"
		}
	}
	tests := []diaglab.Test{
		{Name: "probeF", Cost: 1, Outcome: probe("F")},
		{Name: "probeA", Cost: 2, Outcome: probe("A")},
		{Name: "probeB", Cost: 2, Outcome: probe("B")},
		{Name: "probeG", Cost: 3, Outcome: probe("G")},
		{Name: "fullTrace", Cost: 9, Outcome: func(w int) string {
			return "trace=" + strings.Join(worlds[w].RootSet, "+")
		}},
	}

	policy, err := diaglab.OptimalPolicy(worlds, tests)
	if err != nil {
		panic(err)
	}
	fmt.Printf("候选世界 %d 个，共享预算下最小最坏费用 = %d\n", len(worlds), policy.WorstCost)
	fmt.Println("非预知策略（测试选择只依赖已观测结果）：")
	fmt.Print(policy.Format())

	recs, err := diaglab.Replay(policy, worlds, tests)
	if err != nil {
		panic(err)
	}
	fmt.Println("逐路径重放验证：")
	for _, r := range recs {
		var seq []string
		for _, s := range r.Steps {
			seq = append(seq, fmt.Sprintf("%s=%s(-%d)", s.Test, s.Outcome, s.Cost))
		}
		fmt.Printf("  %s 真实根 {%s}: %s => 识别 {%s} 费用 %d 正确=%v\n",
			r.World, strings.Join(r.Expected, ","), strings.Join(seq, " "),
			strings.Join(r.Identified, ","), r.TotalCost, r.OK)
	}
	worst := diaglab.WorstReplayCost(recs)
	fmt.Printf("重放最坏费用 %d，与动态规划值 %d 一致: %v\n", worst, policy.WorstCost, worst == policy.WorstCost)
}

func main() {
	part1()
	part2()
}
