package checker

import (
	"testing"
	"time"
)

// at 用逻辑日构造时间：at(10) 表示第 10 天。
func at(day int) time.Time {
	return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, day)
}

// 验收边界场景：
//   - 处方 P 在业务时间 10 开立，含甲药、乙药；
//   - 知悉 10 时规则库无甲乙禁忌；
//   - 知悉 20 时指南 v2 更新，将甲乙列为慎用；
//   - 知悉 25 查询：开方合规性 = 合格；风险审计 = 标记追溯风险。
func buildAcceptanceScenario() (*RuleStore, Prescription) {
	store := NewRuleStore()
	// 知悉 20：指南 2024-v2 将甲乙列为慎用，生效时间回溯至 0（适用于历史处方）。
	store.Add(Rule{
		DrugA:            "甲药",
		DrugB:            "乙药",
		Severity:         SeverityCaution,
		GuidelineVersion: "2024-v2",
		ValidFrom:        at(0),
		RecordedAt:       at(20),
	})
	rx := Prescription{ID: "P", IssuedAt: at(10), Drugs: []string{"甲药", "乙药", "丙药"}}
	return store, rx
}

func TestAcceptance_CompliantAtPrescribingTime(t *testing.T) {
	store, rx := buildAcceptanceScenario()

	// 知悉 25 查询开方合规性：判定锚定在开立时刻（业务/知悉均为 10）。
	rep := CheckCompliance(store, rx)
	if !rep.Compliant {
		t.Fatalf("期望开方合规，实际不合规，命中: %v", rep.Findings)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("开方时刻不应命中任何规则，实际命中 %d 条", len(rep.Findings))
	}
	if rep.KnowledgeAsOf != at(10) {
		t.Fatalf("合规判定的知悉时间应锚定在开立时刻 10，实际 %v", rep.KnowledgeAsOf)
	}
}

func TestAcceptance_RetrospectiveRiskFlagged(t *testing.T) {
	store, rx := buildAcceptanceScenario()

	// 知悉 25 的风险审计：用最新规则库重评历史处方。
	rep := AuditRetrospective(store, rx, at(25))
	if !rep.AtRisk {
		t.Fatal("期望审计标记追溯风险，实际未标记")
	}
	if len(rep.RetrospectiveFindings) != 1 {
		t.Fatalf("期望 1 条追溯风险，实际 %d 条", len(rep.RetrospectiveFindings))
	}
	f := rep.RetrospectiveFindings[0]
	if f.Rule.Severity != SeverityCaution || f.Rule.GuidelineVersion != "2024-v2" {
		t.Fatalf("追溯风险应为指南 2024-v2 的慎用规则，实际 %+v", f.Rule)
	}
}

func TestAcceptance_NoRiskBeforeRuleRecorded(t *testing.T) {
	store, rx := buildAcceptanceScenario()

	// 知悉 15（规则尚未录入）时审计：无追溯风险。
	rep := AuditRetrospective(store, rx, at(15))
	if rep.AtRisk {
		t.Fatalf("知悉 15 时不应有追溯风险，实际命中 %v", rep.RetrospectiveFindings)
	}
}

func TestCompliance_ContraindicatedKnownAtPrescribing(t *testing.T) {
	store := NewRuleStore()
	// 知悉 5：甲乙禁用（早于开方）。
	store.Add(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityContraindicated,
		GuidelineVersion: "2024-v1", ValidFrom: at(0), RecordedAt: at(5)})
	rx := Prescription{ID: "P2", IssuedAt: at(10), Drugs: []string{"甲药", "乙药"}}

	rep := CheckCompliance(store, rx)
	if rep.Compliant {
		t.Fatal("开方时已知禁用规则，应判定不合规")
	}
	// 该风险开方时已知，审计不应再计为追溯风险。
	audit := AuditRetrospective(store, rx, at(30))
	if audit.AtRisk {
		t.Fatalf("开方时已知的规则不应计入追溯风险，实际 %v", audit.RetrospectiveFindings)
	}
}

func TestCompliance_CautionStillCompliant(t *testing.T) {
	store := NewRuleStore()
	store.Add(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityCaution,
		GuidelineVersion: "2024-v1", ValidFrom: at(0), RecordedAt: at(5)})
	rx := Prescription{ID: "P3", IssuedAt: at(10), Drugs: []string{"甲药", "乙药"}}

	rep := CheckCompliance(store, rx)
	if !rep.Compliant {
		t.Fatal("慎用命中不应判定为不合规")
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("应记录 1 条慎用命中，实际 %d 条", len(rep.Findings))
	}
}

func TestValidTime_RuleNotYetEffective(t *testing.T) {
	store := NewRuleStore()
	// 规则知悉于 5，但临床生效于 15；处方开立于 10。
	store.Add(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityContraindicated,
		GuidelineVersion: "2024-v1", ValidFrom: at(15), RecordedAt: at(5)})
	rx := Prescription{ID: "P4", IssuedAt: at(10), Drugs: []string{"甲药", "乙药"}}

	rep := CheckCompliance(store, rx)
	if !rep.Compliant {
		t.Fatal("规则尚未临床生效，处方应合规")
	}
}

func TestMultiVersion_GuidelineEvolution(t *testing.T) {
	store := NewRuleStore()
	// v1：甲乙慎用，知悉 5。
	store.Add(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityCaution,
		GuidelineVersion: "2024-v1", ValidFrom: at(0), RecordedAt: at(5)})
	// v2：升级为禁用，知悉 20。
	store.Add(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityContraindicated,
		GuidelineVersion: "2024-v2", ValidFrom: at(0), RecordedAt: at(20)})
	rx := Prescription{ID: "P5", IssuedAt: at(10), Drugs: []string{"甲药", "乙药"}}

	// 开方时仅知悉 v1 慎用：合规但有 1 条命中。
	rep := CheckCompliance(store, rx)
	if !rep.Compliant || len(rep.Findings) != 1 || rep.Findings[0].Rule.GuidelineVersion != "2024-v1" {
		t.Fatalf("开方时应仅命中 v1 慎用，实际 %+v", rep)
	}
	// 知悉 25 审计：v2 禁用为追溯风险。
	audit := AuditRetrospective(store, rx, at(25))
	if !audit.AtRisk || len(audit.RetrospectiveFindings) != 1 {
		t.Fatalf("审计应标记 1 条追溯风险，实际 %+v", audit)
	}
	if WorstSeverity(audit.RetrospectiveFindings) != SeverityContraindicated {
		t.Fatal("追溯风险最高级别应为禁用")
	}
}

func TestMatch_DrugPairUnordered(t *testing.T) {
	store := NewRuleStore()
	store.Add(Rule{DrugA: "乙药", DrugB: "甲药", Severity: SeverityCaution,
		GuidelineVersion: "2024-v1", ValidFrom: at(0), RecordedAt: at(0)})
	rx := Prescription{ID: "P6", IssuedAt: at(10), Drugs: []string{"甲药", "乙药"}}

	rep := CheckCompliance(store, rx)
	if len(rep.Findings) != 1 {
		t.Fatalf("药品对匹配应与顺序无关，实际命中 %d 条", len(rep.Findings))
	}
}

func TestNoMatch_SingleDrug(t *testing.T) {
	store := NewRuleStore()
	store.Add(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityContraindicated,
		GuidelineVersion: "2024-v1", ValidFrom: at(0), RecordedAt: at(0)})
	rx := Prescription{ID: "P7", IssuedAt: at(10), Drugs: []string{"甲药", "丙药"}}

	rep := CheckCompliance(store, rx)
	if !rep.Compliant || len(rep.Findings) != 0 {
		t.Fatalf("仅含甲药不应命中，实际 %+v", rep)
	}
}
