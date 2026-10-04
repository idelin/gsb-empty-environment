package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `有限状态规划器（布尔命题，最低成本计划）

用法:
  fsplanner plan   <problem.json> [plan-out.json]   求解最低成本计划
  fsplanner replay <problem.json> <plan.json>       独立回放并校验计划

问题 JSON 格式:
  {
    "propositions": ["p", "q", ...],        // 最多 20 个
    "initial":      ["p"],                  // 初始为真的命题
    "goal":         ["q"],                  // 目标要求为真的命题
    "goal_neg":     [],                     // 目标要求为假的命题（可选）
    "actions": [
      {"name": "a1", "cost": 2,
       "pre": ["p"], "pre_neg": [],
       "add": ["q"], "del": ["p"]}
    ]
  }
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "plan":
		cmdPlan(os.Args[2:])
	case "replay":
		cmdReplay(os.Args[2:])
	default:
		usage()
	}
}

func cmdPlan(args []string) {
	if len(args) < 1 || len(args) > 2 {
		usage()
	}
	p, err := LoadProblem(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载问题失败:", err)
		os.Exit(1)
	}
	res := Solve(p)
	if !res.Found {
		fmt.Print(p.UnreachableSummary(res, 50))
		os.Exit(1)
	}
	fmt.Printf("找到最低成本计划，总成本 %d，共 %d 步:\n", res.Plan.Cost, len(res.Plan.Steps))
	for i, s := range res.Plan.Steps {
		fmt.Printf("  %3d. %s\n", i+1, s)
	}
	if len(args) == 2 {
		data, err := json.MarshalIndent(res.Plan, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "序列化计划失败:", err)
			os.Exit(1)
		}
		if err := writeFile(args[1], data); err != nil {
			fmt.Fprintln(os.Stderr, "写入计划失败:", err)
			os.Exit(1)
		}
		fmt.Println("计划已保存到", args[1])
	}
}

func cmdReplay(args []string) {
	if len(args) != 2 {
		usage()
	}
	p, err := LoadProblem(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载问题失败:", err)
		os.Exit(1)
	}
	plan, err := LoadPlan(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "加载计划失败:", err)
		os.Exit(1)
	}
	rep, err := Replay(p, plan)
	for _, st := range rep.Steps {
		fmt.Printf("  %3d. %-20s 成本 %-4d %s -> %s\n", st.Index, st.Action, st.Cost, st.Before, st.After)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "回放失败:", err)
		os.Exit(1)
	}
	fmt.Printf("回放成功: 目标达成，总成本 %d，共 %d 步。\n", rep.TotalCost, len(rep.Steps))
}
