package rxcheck

import "testing"

// 验收边界场景：
//   - 处方 P 在业务时间 10 开立，含甲药、乙药；
//   - 知悉时间 10：规则库对甲乙无禁忌；
//   - 知悉时间 20：指南 v2 更新，甲乙列为慎用；
//   - 知悉时间 25 查询：开方合规性 = 合格；风险审计 = 标记追溯风险。
func buildAcceptanceScenario() (*RuleStore, Prescription) {
	store := NewRuleStore()
	// 知悉时间 20：指南 v2 将 甲药+乙药 列为慎用。
	store.AddRule(Rule{
		DrugA: "甲药", DrugB: "乙药",
		Severity: SeverityCaution, Guideline: "指南v2", KnownFrom: 20,
	})
	p := Prescription{ID: "P", Drugs: []string{"甲药", "乙药"}, BusinessTime: 10}
	return store, p
}

func TestAcceptance_ComplianceAtPrescriptionTime(t *testing.T) {
	store, p := buildAcceptanceScenario()

	rep := CheckCompliance(store, p)
	if !rep.Compliant {
		t.Fatalf("期望开方合规性为合格，实际不合格：%v", rep.Violations)
	}
	if rep.EvaluatedAt != 10 {
		t.Fatalf("合规性评估应使用开立时刻的知悉时间 10，实际 %d", rep.EvaluatedAt)
	}
	if len(rep.Violations) != 0 {
		t.Fatalf("合格处方不应有违规项，实际 %v", rep.Violations)
	}
}

func TestAcceptance_RetrospectiveRiskAtQueryTime(t *testing.T) {
	store, p := buildAcceptanceScenario()

	rep := AuditRisk(store, p, 25)
	if !rep.HasRetrospectiveRisk {
		t.Fatal("期望风险审计标记追溯风险，实际未标记")
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("期望 1 条追溯风险提示，实际 %d 条", len(rep.Findings))
	}
	f := rep.Findings[0]
	if f.DrugA != "乙药" || f.DrugB != "甲药" { // 药品对按字典序输出
		t.Fatalf("风险提示药品对错误：%s + %s", f.DrugA, f.DrugB)
	}
	if f.Severity != SeverityCaution {
		t.Fatalf("期望级别为慎用，实际 %s", f.Severity)
	}
	if f.Guideline != "指南v2" {
		t.Fatalf("期望依据指南v2，实际 %s", f.Guideline)
	}
}

func TestAcceptance_ComplianceAndRiskCoexist(t *testing.T) {
	store, p := buildAcceptanceScenario()

	comp := CheckCompliance(store, p)
	risk := AuditRisk(store, p, 25)
	if !comp.Compliant || !risk.HasRetrospectiveRisk {
		t.Fatalf("验收边界不满足：合规=%v，追溯风险=%v", comp.Compliant, risk.HasRetrospectiveRisk)
	}
}

func TestSeverityAsOf_VersionedRules(t *testing.T) {
	store := NewRuleStore()
	store.AddRule(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityCaution, Guideline: "指南v2", KnownFrom: 20})
	store.AddRule(Rule{DrugA: "乙药", DrugB: "甲药", Severity: SeverityContraindicated, Guideline: "指南v3", KnownFrom: 30}) // 药品顺序无关

	cases := []struct {
		knownAt  int
		expected Severity
	}{
		{10, SeverityNone},
		{20, SeverityCaution},
		{25, SeverityCaution},
		{30, SeverityContraindicated},
		{99, SeverityContraindicated},
	}
	for _, c := range cases {
		if got := store.SeverityAsOf("甲药", "乙药", c.knownAt); got != c.expected {
			t.Errorf("知悉时间 %d：期望 %s，实际 %s", c.knownAt, c.expected, got)
		}
	}
}

func TestCheckCompliance_ViolationWhenRuleKnownBeforePrescription(t *testing.T) {
	store := NewRuleStore()
	store.AddRule(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityContraindicated, Guideline: "指南v1", KnownFrom: 5})
	p := Prescription{ID: "P2", Drugs: []string{"甲药", "乙药", "丙药"}, BusinessTime: 10}

	rep := CheckCompliance(store, p)
	if rep.Compliant {
		t.Fatal("规则先于处方知悉，期望判定不合格")
	}
	if len(rep.Violations) != 1 || rep.Violations[0].Severity != SeverityContraindicated {
		t.Fatalf("违规项不符合预期：%v", rep.Violations)
	}
}

func TestAuditRisk_NoRiskWhenSeverityUnchanged(t *testing.T) {
	store := NewRuleStore()
	// 开立前已列为慎用，查询时仍只是慎用：有发现但无追溯风险。
	store.AddRule(Rule{DrugA: "甲药", DrugB: "乙药", Severity: SeverityCaution, Guideline: "指南v1", KnownFrom: 5})
	p := Prescription{ID: "P3", Drugs: []string{"甲药", "乙药"}, BusinessTime: 10}

	rep := AuditRisk(store, p, 25)
	if rep.HasRetrospectiveRisk {
		t.Fatal("级别未上升，不应标记追溯风险")
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("当前规则下仍应列出 1 条发现，实际 %d", len(rep.Findings))
	}
}

func TestAuditRisk_NoFindingsWithoutRules(t *testing.T) {
	store := NewRuleStore()
	p := Prescription{ID: "P4", Drugs: []string{"甲药", "乙药"}, BusinessTime: 10}

	rep := AuditRisk(store, p, 25)
	if rep.HasRetrospectiveRisk || len(rep.Findings) != 0 {
		t.Fatalf("无规则时不应有任何风险输出：%+v", rep)
	}
}

func TestDrugPairs_DedupAndUnordered(t *testing.T) {
	p := Prescription{ID: "P5", Drugs: []string{"乙药", "甲药", "丙药", "甲药"}, BusinessTime: 1}
	pairs := p.drugPairs()
	if len(pairs) != 3 {
		t.Fatalf("3 种药品应产生 3 个药品对，实际 %d：%v", len(pairs), pairs)
	}
}
