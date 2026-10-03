// 运行样例：演示验收边界场景的双时态判定。
package main

import (
	"fmt"

	"rxcheck"
)

func main() {
	store := rxcheck.NewRuleStore()

	// 知悉时间 20：临床指南 v2 发布，将 甲药+乙药 列为慎用。
	store.AddRule(rxcheck.Rule{
		DrugA: "甲药", DrugB: "乙药",
		Severity: rxcheck.SeverityCaution, Guideline: "指南v2", KnownFrom: 20,
	})

	// 处方 P 在业务时间 10 开立，含甲药和乙药。
	p := rxcheck.Prescription{ID: "P", Drugs: []string{"甲药", "乙药"}, BusinessTime: 10}

	// 知悉时间 25 查询：
	// 1) 开方合规性：按开立时刻（知悉 10）的规则版本 -> 合格。
	comp := rxcheck.CheckCompliance(store, p)
	fmt.Print(rxcheck.FormatCompliance(comp))

	// 2) 追溯风险审计：按查询时刻（知悉 25）的最新规则 -> 标记追溯风险。
	risk := rxcheck.AuditRisk(store, p, 25)
	fmt.Print(rxcheck.FormatRisk(risk))
}
