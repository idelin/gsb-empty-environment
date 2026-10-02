// demo 复现验收边界场景：
// 处方 P 于业务时间 10 开立（含甲药、乙药）；知悉 20 指南更新将甲乙列为慎用；
// 知悉 25 查询开方合规性为合格，但风险审计标记追溯风险。
package main

import (
	"fmt"
	"time"

	"rxcheck/checker"
)

func at(day int) time.Time {
	return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, day)
}

func day(t time.Time) int {
	return int(t.Sub(at(0)).Hours()/24 + 0.5)
}

func main() {
	store := checker.NewRuleStore()

	// 知悉 20：临床指南 2024-v2 将甲药+乙药列为慎用。
	store.Add(checker.Rule{
		DrugA:            "甲药",
		DrugB:            "乙药",
		Severity:         checker.SeverityCaution,
		GuidelineVersion: "2024-v2",
		ValidFrom:        at(0),
		RecordedAt:       at(20),
	})

	// 处方 P：业务时间 10 开立，含甲药、乙药。
	rx := checker.Prescription{ID: "P", IssuedAt: at(10), Drugs: []string{"甲药", "乙药", "丙药"}}

	fmt.Println("=== 多版本医疗处方配伍禁忌检查器（双时态判定）===")
	fmt.Printf("处方 %s：开立于业务时间 %d，药品 %v\n", rx.ID, day(rx.IssuedAt), rx.Drugs)
	fmt.Println("规则库：知悉 20 录入指南 2024-v2「甲药+乙药 慎用」")
	fmt.Println()

	// 知悉 25：开方合规性判定（锚定开立时刻的知识）。
	comp := checker.CheckCompliance(store, rx)
	fmt.Printf("[知悉 25 查询] 开方合规性判定（业务时间 %d / 知悉时间 %d）：\n",
		day(comp.IssuedAt), day(comp.KnowledgeAsOf))
	if comp.Compliant {
		fmt.Println("  结论：合格 ✓（开立当时规则库无甲乙禁忌）")
	} else {
		fmt.Println("  结论：不合格 ✗")
	}
	for _, f := range comp.Findings {
		fmt.Println("  命中:", f)
	}
	fmt.Println()

	// 知悉 25：追溯风险审计（使用当前最新规则库）。
	audit := checker.AuditRetrospective(store, rx, at(25))
	fmt.Printf("[知悉 25 查询] 追溯风险审计（最新规则库）：\n")
	if audit.AtRisk {
		fmt.Println("  结论：存在追溯风险 ⚠")
	} else {
		fmt.Println("  结论：无追溯风险")
	}
	for _, f := range audit.RetrospectiveFindings {
		fmt.Println("  风险:", f)
	}
}
