package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	input := flag.String("input", "", "输入 JSON 文件路径（工序、检验结果与返工链定义）")
	asJSON := flag.Bool("json", false, "以 JSON 输出完整报告")
	flag.Parse()
	if *input == "" {
		fmt.Fprintln(os.Stderr, "用法: reworksched -input plan.json [-json]")
		os.Exit(2)
	}
	raw, err := ioutil.ReadFile(*input)
	if err != nil {
		fatal("读取输入失败", err)
	}
	var p Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		fatal("解析输入 JSON 失败", err)
	}
	acts, err := p.Instance()
	if err != nil {
		fatal("构建工序实例图失败", err)
	}
	sol, err := SolveOptimal(acts, p.Resources)
	if err != nil {
		fatal("求解失败", err)
	}
	rep, err := BuildReport(acts, p.Resources, sol)
	if err != nil {
		fatal("核验失败", err)
	}
	if *asJSON {
		out, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fatal("序列化报告失败", err)
		}
		fmt.Println(string(out))
		return
	}
	printText(rep)
}

func fatal(stage string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", stage, err)
	os.Exit(1)
}

func printText(r Report) {
	fmt.Printf("总完工时间 (makespan): %d", r.Makespan)
	if r.Optimal {
		fmt.Println("  [已证明最短]")
	} else {
		fmt.Println("  [搜索截断，未证明最优]")
	}
	fmt.Printf("工序实例数: %d\n\n", r.Instances)

	fmt.Println("== 排程 ==")
	for _, e := range r.Entries {
		kind := "原工序"
		if e.IsRework {
			kind = fmt.Sprintf("返工(源自 %s)", e.Origin)
		}
		fmt.Printf("  %-12s [%3d, %3d)  %s\n", e.ID, e.Start, e.Finish, kind)
	}

	fmt.Println("\n== 资源占用区间 ==")
	cur := ""
	for _, u := range r.Usage {
		if u.Resource != cur {
			cur = u.Resource
			fmt.Printf("  资源 %s:\n", cur)
		}
		fmt.Printf("    [%3d, %3d)  %-12s 负载 %d\n", u.Start, u.Finish, u.Task, u.Load)
	}

	fmt.Println("\n== 依赖核验 ==")
	for _, c := range r.DepChecks {
		mark := "OK"
		if !c.OK {
			mark = "违反"
		}
		fmt.Printf("  %-12s -> %-12s 前驱完成 %3d <= 后继开工 %3d  [%s]\n",
			c.Predecessor, c.Successor, c.PredFinish, c.SuccStart, mark)
	}
	status := func(ok bool) string {
		if ok {
			return "通过"
		}
		return "失败"
	}
	fmt.Printf("\n依赖核验: %s    资源容量核验: %s\n", status(r.DepsOK), status(r.ResourceOK))
}
