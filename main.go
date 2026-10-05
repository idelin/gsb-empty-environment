package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"strings"
)

//go:embed demo.json
var demoJSON []byte

func main() {
	input := flag.String("input", "", "JSON 实例文件路径（缺省时运行内置演示实例）")
	flag.Parse()

	var inst Instance
	var err error
	if *input != "" {
		inst, err = LoadInstance(*input)
	} else {
		inst, err = ParseInstance(demoJSON)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取实例失败:", err)
		os.Exit(1)
	}

	res, err := Solve(inst)
	if err != nil {
		fmt.Fprintln(os.Stderr, "求解失败:", err)
		os.Exit(1)
	}
	fmt.Print(formatReport(res))
}

func formatReport(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "预算: %d\n", r.Budget)
	fmt.Fprintf(&b, "选中项目数: %d\n", len(r.Selected))
	fmt.Fprintf(&b, "%-8s %-16s %10s %10s\n", "ID", "名称", "成本", "固定收益")
	for _, p := range r.Selected {
		fmt.Fprintf(&b, "%-8s %-16s %10d %10d\n", p.ID, p.Name, p.Cost, p.Value)
	}
	fmt.Fprintf(&b, "%-8s %-16s %10d %10d\n", "", "合计", r.TotalCost, r.BaseValue)
	fmt.Fprintf(&b, "\n协同收益分解（成对计入，含负协同）:\n")
	if len(r.Synergies) == 0 {
		fmt.Fprintf(&b, "  （无）\n")
	}
	for _, sy := range r.Synergies {
		fmt.Fprintf(&b, "  %-8s × %-8s %+d\n", sy.A, sy.B, sy.Value)
	}
	fmt.Fprintf(&b, "协同收益合计: %+d\n", r.SynergyValue)
	fmt.Fprintf(&b, "\n总成本: %d / 预算 %d（剩余 %d）\n", r.TotalCost, r.Budget, r.Budget-r.TotalCost)
	fmt.Fprintf(&b, "总收益: %d（固定收益 %d + 协同收益 %+d）\n", r.TotalValue, r.BaseValue, r.SynergyValue)
	fmt.Fprintf(&b, "\n最优性上界证据:\n")
	fmt.Fprintf(&b, "  根节点上界: %d（任何可行解的收益均不超过该值）\n", r.RootUpperBound)
	fmt.Fprintf(&b, "  最优解收益: %d\n", r.TotalValue)
	fmt.Fprintf(&b, "  搜索节点: %d，其中按上界剪枝: %d\n", r.NodesExplored, r.NodesPruned)
	fmt.Fprintf(&b, "  结论: 分支定界已穷尽搜索空间，所有被剪子树的上界均不超过最优解收益，故该组合为全局最优。\n")
	return b.String()
}
