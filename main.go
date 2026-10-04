package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func main() {
	jsonOut := flag.Bool("json", false, "以 JSON 输出结果")
	flag.Parse()

	var data []byte
	var err error
	if flag.NArg() > 0 {
		data, err = ioutil.ReadFile(flag.Arg(0))
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("读取输入失败: ", err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal("解析输入 JSON 失败: ", err)
	}
	if err := in.validate(); err != nil {
		fatal("输入不合法: ", err)
	}
	in.setDefaults()

	solver := NewSolver(in)
	solver.Solve()
	res := solver.Replay()

	if *jsonOut {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return
	}
	printReport(res)
}

func printReport(res Result) {
	fmt.Printf("总兑换价值: %d\n", res.TotalValue)
	fmt.Printf("总消耗积分: %d\n", res.TotalSpent)
	fmt.Printf("到期损失:   %d\n", res.TotalExpired)
	fmt.Printf("返还作废:   %d (最后一个时段产生的返还无法使用)\n", res.ForfeitedRebate)
	if res.Truncated {
		fmt.Println("警告: 状态空间过大，搜索被截断，结果可能不是全局最优")
	}
	for _, p := range res.Periods {
		fmt.Printf("\n时段 %d:\n", p.Period)
		for _, a := range p.Arrivals {
			fmt.Printf("  返还到账: %s = %d 分 (来源 %s, 第 %d 时段到期)\n",
				a.BatchID, a.Amount, a.Source, a.ExpirePeriod)
		}
		if len(p.Redemptions) == 0 {
			fmt.Println("  (无兑换)")
		}
		for _, r := range p.Redemptions {
			line := fmt.Sprintf("  兑换 %s x%d  消耗 %d 分  价值 %d",
				r.OfferID, r.Units, r.Points, r.Value)
			if r.Rebate > 0 {
				line += fmt.Sprintf("  返还 %d 分(下一时段可用)", r.Rebate)
			}
			fmt.Println(line)
		}
		for _, d := range p.Deductions {
			fmt.Printf("  扣减: %s -> %s  -%d 分\n", d.BatchID, d.OfferID, d.Amount)
		}
		for _, e := range p.Expired {
			fmt.Printf("  到期损失: %s 损失 %d 分\n", e.BatchID, e.Amount)
		}
	}
}

func fatal(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}
