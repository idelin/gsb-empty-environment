package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type deletedRowJSON struct {
	Ref     string    `json:"ref"`
	Weight  int64     `json:"weight"`
	Reasons []*Reason `json:"reasons"`
}

type verificationJSON struct {
	Passed     bool     `json:"passed"`
	Violations []string `json:"violations,omitempty"`
}

type reportJSON struct {
	Optimal       bool             `json:"optimal"`
	DeletedWeight int64            `json:"deleted_weight"`
	KeptWeight    int64            `json:"kept_weight"`
	Deleted       []deletedRowJSON `json:"deleted"`
	Kept          []string         `json:"kept"`
	Verification  verificationJSON `json:"verification"`
}

func printJSONReport(m *Model, res *Result, reasons map[int][]*Reason, violations []string) {
	rep := reportJSON{
		Optimal:       res.Optimal,
		DeletedWeight: res.DeletedWeight,
		KeptWeight:    res.KeptWeight,
		Verification: verificationJSON{
			Passed:     len(violations) == 0,
			Violations: violations,
		},
	}
	for _, i := range res.DeletedRows {
		rep.Deleted = append(rep.Deleted, deletedRowJSON{
			Ref:     m.Rows[i].Ref(),
			Weight:  m.Rows[i].Weight,
			Reasons: reasons[i],
		})
	}
	for _, i := range res.KeptRows {
		rep.Kept = append(rep.Kept, m.Rows[i].Ref())
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(b))
}

func writeReasonTree(b *strings.Builder, r *Reason, indent string) {
	fmt.Fprintf(b, "%s- %s\n", indent, r.Message)
	for _, c := range r.Children {
		writeReasonTree(b, c, indent+"  ")
	}
}

func printTextReport(m *Model, res *Result, reasons map[int][]*Reason, violations []string) {
	var b strings.Builder

	optimalNote := "全局最优"
	if !res.Optimal {
		optimalNote = "搜索被截断，为当前最优可行解（非保证最优）"
	}
	fmt.Fprintf(&b, "== 删除计划（%s）==\n", optimalNote)
	fmt.Fprintf(&b, "删除 %d 行，删除权重合计 %d；保留 %d 行，保留权重合计 %d\n\n",
		len(res.DeletedRows), res.DeletedWeight, len(res.KeptRows), res.KeptWeight)

	b.WriteString("-- 删除行及原因闭包 --\n")
	if len(res.DeletedRows) == 0 {
		b.WriteString("（无需删除任何行）\n")
	}
	for _, i := range res.DeletedRows {
		row := m.Rows[i]
		fmt.Fprintf(&b, "✗ %s (weight=%d)\n", row.Ref(), row.Weight)
		for _, r := range reasons[i] {
			writeReasonTree(&b, r, "    ")
		}
	}

	b.WriteString("\n-- 保留表 --\n")
	byTable := map[string][]string{}
	var tableOrder []string
	for _, i := range res.KeptRows {
		row := m.Rows[i]
		if _, ok := byTable[row.Table]; !ok {
			tableOrder = append(tableOrder, row.Table)
		}
		byTable[row.Table] = append(byTable[row.Table], fmt.Sprintf("%s(w=%d)", row.ID, row.Weight))
	}
	for _, t := range tableOrder {
		fmt.Fprintf(&b, "%s: %s\n", t, strings.Join(byTable[t], ", "))
	}

	b.WriteString("\n-- 保留表核验 --\n")
	if len(violations) == 0 {
		b.WriteString("[PASS] 唯一键约束：全部满足\n")
		b.WriteString("[PASS] 外键约束（含级联闭包）：全部满足\n")
		b.WriteString("[PASS] 行间冲突规则：全部满足\n")
	} else {
		for _, v := range violations {
			fmt.Fprintf(&b, "[FAIL] %s\n", v)
		}
	}
	fmt.Print(b.String())
}
