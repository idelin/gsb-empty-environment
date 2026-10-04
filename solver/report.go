package solver

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
)

// IdleItem 一笔未动用资金（含过期闲置与无有效期的剩余）。
type IdleItem struct {
	Owner   string `json:"owner"`   // 来源 ID 或配捐规则 ID
	Kind    string `json:"kind"`    // source / match_rule
	Amount  Cents  `json:"amount"`  // 剩余金额
	Expires string `json:"expires"` // 空表示无有效期
	Expired bool   `json:"expired"` // 是否计入“过期闲置”
}

// Report 求解结果。
type Report struct {
	AsOf           string     `json:"as_of"`
	MaxProjects    int        `json:"max_projects"`
	FundedProjects []string   `json:"funded_projects"`
	Unfunded       []string   `json:"unfunded_projects"`
	TotalBenefit   Cents      `json:"total_benefit"`
	TotalDirect    Cents      `json:"total_direct"`
	TotalMatched   Cents      `json:"total_matched"`
	ExpiredIdle    Cents      `json:"expired_idle"`
	Flows          []Flow     `json:"flows"`
	Idle           []IdleItem `json:"idle"`
}

func (s *Solver) buildReport() *Report {
	rep := &Report{
		AsOf:        s.in.AsOf,
		MaxProjects: s.maxProjects,
		Flows:       s.bestFlows,
	}
	for i, f := range s.bestFunded {
		if f {
			rep.FundedProjects = append(rep.FundedProjects, s.in.Projects[i].ID)
		} else {
			rep.Unfunded = append(rep.Unfunded, s.in.Projects[i].ID)
		}
	}
	sort.Strings(rep.FundedProjects)
	sort.Strings(rep.Unfunded)
	rep.TotalBenefit = s.bestBenefit
	for _, fl := range s.bestFlows {
		if fl.Kind == "direct" {
			rep.TotalDirect += fl.Amount
		} else {
			rep.TotalMatched += fl.Amount
		}
	}
	for i, src := range s.in.Sources {
		if s.bestSrcBal[i] <= 0 {
			continue
		}
		rep.Idle = append(rep.Idle, IdleItem{
			Owner: src.ID, Kind: "source", Amount: s.bestSrcBal[i],
			Expires: src.Expires, Expired: src.Expires != "",
		})
		if src.Expires != "" {
			rep.ExpiredIdle += s.bestSrcBal[i]
		}
	}
	for i, r := range s.in.MatchRules {
		if s.bestRuleCap[i] <= 0 {
			continue
		}
		rep.Idle = append(rep.Idle, IdleItem{
			Owner: r.ID, Kind: "match_rule", Amount: s.bestRuleCap[i],
			Expires: r.Expires, Expired: r.Expires != "",
		})
		if r.Expires != "" {
			rep.ExpiredIdle += s.bestRuleCap[i]
		}
	}
	return rep
}

// FormatMoney 以千分位格式化金额（元）。
func FormatMoney(c Cents) string {
	neg := c < 0
	if neg {
		c = -c
	}
	intPart := c / 100
	frac := c % 100
	digits := []byte(fmt.Sprintf("%d", intPart))
	var sb strings.Builder
	if neg {
		sb.WriteByte('-')
	}
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte(d)
	}
	fmt.Fprintf(&sb, ".%02d", frac)
	return sb.String()
}

// RenderText 渲染人类可读的中文报告。
func RenderText(rep *Report) string {
	var sb strings.Builder
	sb.WriteString("========== 资金分配方案 ==========\n")
	fmt.Fprintf(&sb, "决策基准日: %s    资助项目上限: %d\n", rep.AsOf, rep.MaxProjects)
	fmt.Fprintf(&sb, "总项目收益: %s\n", FormatMoney(rep.TotalBenefit))
	fmt.Fprintf(&sb, "已达标项目(%d): %s\n", len(rep.FundedProjects), strings.Join(rep.FundedProjects, ", "))
	if len(rep.Unfunded) > 0 {
		fmt.Fprintf(&sb, "未达标项目(%d): %s\n", len(rep.Unfunded), strings.Join(rep.Unfunded, ", "))
	}
	fmt.Fprintf(&sb, "直接拨款合计: %s    配捐合计: %s\n", FormatMoney(rep.TotalDirect), FormatMoney(rep.TotalMatched))
	fmt.Fprintf(&sb, "过期闲置金额: %s\n", FormatMoney(rep.ExpiredIdle))

	sb.WriteString("\n---------- 资金流向 ----------\n")
	w := tabwriter.NewWriter(&sb, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "#\t类型\t来源\t去向\t金额(元)\t触发依据")
	for i, f := range rep.Flows {
		kind := "直接拨款"
		if f.Kind == "match" {
			kind = "配捐"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			i+1, kind, f.From, f.To, FormatMoney(f.Amount), f.Reason)
	}
	w.Flush()

	if len(rep.Idle) > 0 {
		sb.WriteString("\n---------- 闲置明细 ----------\n")
		w = tabwriter.NewWriter(&sb, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "主体\t类型\t剩余金额(元)\t有效期\t是否过期闲置")
		for _, it := range rep.Idle {
			kind := "资金来源"
			if it.Kind == "match_rule" {
				kind = "配捐额度"
			}
			expires := it.Expires
			if expires == "" {
				expires = "无"
			}
			expired := "否"
			if it.Expired {
				expired = "是"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", it.Owner, kind, FormatMoney(it.Amount), expires, expired)
		}
		w.Flush()
	}
	return sb.String()
}
