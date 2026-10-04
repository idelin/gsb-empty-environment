// 保质期配货规划器 CLI。
// 用法:
//
//	planner [-json] <input.json>   从文件读取
//	planner [-json] < input.json   从标准输入读取
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"shelflife-planner/planner"
)

func main() {
	asJSON := flag.Bool("json", false, "以 JSON 输出结果")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() > 1 {
		fatal(fmt.Errorf("至多指定一个输入文件"))
	}
	if flag.NArg() == 1 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}

	var in planner.Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fatal(fmt.Errorf("解析输入 JSON 失败: %w", err))
	}
	res, err := planner.Solve(&in)
	if err != nil {
		fatal(err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fatal(err)
		}
		return
	}
	printText(&in, res)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

func printText(in *planner.Input, res *planner.Result) {
	byDay := make(map[int][]planner.Flow)
	for _, f := range res.Flows {
		byDay[f.Day] = append(byDay[f.Day], f)
	}
	fmt.Println("== 每日流向 ==")
	for t := 1; t <= in.Days; t++ {
		flows := byDay[t]
		sort.Slice(flows, func(i, j int) bool { return flows[i].BatchID < flows[j].BatchID })
		fmt.Printf("第 %d 天 (需求 %d, 额度 %d):\n", t, in.Demand[t-1], in.Quota[t-1])
		if len(flows) == 0 {
			fmt.Println("  (无调拨)")
		}
		var dayTotal int64
		for _, f := range flows {
			fmt.Printf("  批次 %-8s -> %d\n", f.BatchID, f.Qty)
			dayTotal += f.Qty
		}
		fmt.Printf("  当日满足 %d, 不可满足需求 %d\n", dayTotal, res.Unmet[t-1])
	}
	fmt.Println("== 汇总 ==")
	fmt.Printf("不可满足需求合计: %d\n", res.TotalUnmet)
	fmt.Printf("报废合计: %d\n", res.TotalWaste)
	for _, w := range res.Waste {
		fmt.Printf("  批次 %-8s 报废 %d\n", w.BatchID, w.Qty)
	}
	fmt.Printf("总调拨成本: %d\n", res.TotalCost)
}
