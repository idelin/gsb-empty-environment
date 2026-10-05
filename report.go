package main

import (
	"fmt"
	"io"
)

// printReport 输出人类可读的分配方案。
func printReport(w io.Writer, r *Result) {
	fmt.Fprintf(w, "===== 资金分配方案 =====\n")
	fmt.Fprintf(w, "总项目收益: %.2f | 过期闲置金额: %.2f | 达标项目: %d 个（上限 %d）\n\n",
		r.Benefit, r.ExpiredIdle, len(r.Funded), r.MaxProjects)

	fmt.Fprintf(w, "【达标项目】\n")
	if len(r.Funded) == 0 {
		fmt.Fprintf(w, "  （无）\n")
	}
	for _, p := range r.Funded {
		fmt.Fprintf(w, "  %s %s：目标 %.2f，实拨 %.2f（基础资金 %.2f + 配捐 %.2f），收益 %.2f\n",
			p.ID, p.Name, p.Goal, p.Funded, p.BaseFund, p.Funded-p.BaseFund, p.Benefit)
	}

	fmt.Fprintf(w, "\n【资金流向】\n")
	if len(r.Flows) == 0 {
		fmt.Fprintf(w, "  （无）\n")
	}
	for _, f := range r.Flows {
		kind := "直接"
		if f.Type == "match" {
			kind = "配捐"
		}
		fmt.Fprintf(w, "  %s(%s) --%.2f--> %s(%s)  [%s]\n", f.From, f.FromName, f.Amount, f.To, f.ToName, kind)
		fmt.Fprintf(w, "    依据: %s\n", f.Basis)
	}

	fmt.Fprintf(w, "\n【未达标项目】\n")
	if len(r.Unfunded) == 0 {
		fmt.Fprintf(w, "  （无）\n")
	}
	for _, p := range r.Unfunded {
		fmt.Fprintf(w, "  %s %s：目标 %.2f，收益 %.2f —— 未入选（受资金/资格/项目数上限约束，整笔目标无法纳入最优方案）\n",
			p.ID, p.Name, p.Goal, p.Benefit)
	}

	fmt.Fprintf(w, "\n【闲置资金】\n")
	if len(r.Idle) == 0 {
		fmt.Fprintf(w, "  （无）\n")
	}
	for _, f := range r.Idle {
		note := "无有效期，不计入过期闲置"
		if f.Expired {
			note = "过期闲置"
		}
		fmt.Fprintf(w, "  %s(%s)：剩余 %.2f（%s）\n", f.Source, f.SourceName, f.Amount, note)
	}
}
