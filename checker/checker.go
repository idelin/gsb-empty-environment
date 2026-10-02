// Package checker 实现多版本医疗处方配伍禁忌检查器。
//
// 采用双时态（bitemporal）模型：
//   - 业务时间（valid time）：处方开立时间、规则临床生效时间。
//   - 知悉时间（transaction time）：规则被录入规则库的时间。
//
// 由此支持两类判定：
//  1. 开方合规性：以处方开立时刻已知的规则判定历史处方当时是否合规。
//  2. 追溯风险审计：以当前最新规则库重新评估历史处方，标记当时未知、
//     现在已知的配伍风险。
package checker

import (
	"fmt"
	"sort"
	"time"
)

// Severity 表示配伍禁忌的严重级别。
type Severity int

const (
	// SeverityCaution 慎用：需医师评估后使用。
	SeverityCaution Severity = iota + 1
	// SeverityContraindicated 禁用：禁止联用。
	SeverityContraindicated
)

func (s Severity) String() string {
	switch s {
	case SeverityCaution:
		return "慎用"
	case SeverityContraindicated:
		return "禁用"
	default:
		return "未知"
	}
}

// Rule 是一条药品配伍禁忌规则，携带双时态时间戳。
type Rule struct {
	DrugA            string    // 药品甲（无序对）
	DrugB            string    // 药品乙（无序对）
	Severity         Severity  // 禁忌级别
	GuidelineVersion string    // 临床指南版本（多版本规则库）
	ValidFrom        time.Time // 业务时间：规则临床生效时间
	RecordedAt       time.Time // 知悉时间：规则录入规则库的时间
}

// RuleStore 是只追加（append-only）的多版本规则库，保留全部历史。
type RuleStore struct {
	rules []Rule
}

// NewRuleStore 创建空规则库。
func NewRuleStore() *RuleStore { return &RuleStore{} }

// Add 追加一条规则。历史规则永不修改或删除，保证可重放任意知悉时点。
func (s *RuleStore) Add(r Rule) {
	s.rules = append(s.rules, r)
}

// Snapshot 返回在指定知悉时间已录入的全部规则（事务时间切片）。
func (s *RuleStore) Snapshot(knowledgeAsOf time.Time) []Rule {
	var out []Rule
	for _, r := range s.rules {
		if !r.RecordedAt.After(knowledgeAsOf) {
			out = append(out, r)
		}
	}
	return out
}

// Prescription 是一张处方，IssuedAt 为其业务时间。
type Prescription struct {
	ID       string
	IssuedAt time.Time
	Drugs    []string
}

// contains 判断处方是否包含某药品。
func (p Prescription) contains(drug string) bool {
	for _, d := range p.Drugs {
		if d == drug {
			return true
		}
	}
	return false
}

// Finding 表示一次规则命中。
type Finding struct {
	Rule Rule
}

func (f Finding) String() string {
	return fmt.Sprintf("%s + %s [%s, 指南版本 %s, 生效于 %s, 知悉于 %s]",
		f.Rule.DrugA, f.Rule.DrugB, f.Rule.Severity,
		f.Rule.GuidelineVersion,
		f.Rule.ValidFrom.Format("2006-01-02"),
		f.Rule.RecordedAt.Format("2006-01-02"))
}

// match 判断规则是否命中处方：药品对同时出现，且处方业务时间不早于规则生效时间。
func match(rx Prescription, r Rule) bool {
	return rx.contains(r.DrugA) && rx.contains(r.DrugB) && !rx.IssuedAt.Before(r.ValidFrom)
}

// ComplianceReport 是开方合规性判定结果。
type ComplianceReport struct {
	PrescriptionID string
	IssuedAt       time.Time // 判定时使用的业务时间
	KnowledgeAsOf  time.Time // 判定时使用的知悉时间
	Compliant      bool
	Findings       []Finding
}

// CheckCompliance 判定处方在开立当时是否合规：
// 业务时间与知悉时间均锚定在处方开立时刻，即"用当时的知识评当时的方"。
func CheckCompliance(store *RuleStore, rx Prescription) ComplianceReport {
	rep := ComplianceReport{
		PrescriptionID: rx.ID,
		IssuedAt:       rx.IssuedAt,
		KnowledgeAsOf:  rx.IssuedAt,
		Compliant:      true,
	}
	for _, r := range store.Snapshot(rx.IssuedAt) {
		if match(rx, r) {
			rep.Findings = append(rep.Findings, Finding{Rule: r})
		}
	}
	// 存在"禁用"命中则不合规；"慎用"命中不影响合规结论，但会记录。
	for _, f := range rep.Findings {
		if f.Rule.Severity == SeverityContraindicated {
			rep.Compliant = false
			break
		}
	}
	return rep
}

// RiskReport 是追溯风险审计结果。
type RiskReport struct {
	PrescriptionID string
	AuditedAt      time.Time // 审计使用的知悉时间（通常为当前）
	AtRisk         bool
	// RetrospectiveFindings 仅包含开方时未知悉、审计时已知悉的命中，
	// 即"追溯风险"。
	RetrospectiveFindings []Finding
}

// AuditRetrospective 用审计时点（通常为当前最新）的规则库重新评估历史处方，
// 标记开方当时未知、现在已知的配伍风险。
func AuditRetrospective(store *RuleStore, rx Prescription, knowledgeAsOf time.Time) RiskReport {
	rep := RiskReport{PrescriptionID: rx.ID, AuditedAt: knowledgeAsOf}
	for _, r := range store.Snapshot(knowledgeAsOf) {
		// 只报告开方之后才知悉的规则：开方时已知的规则属于当时已接受的风险。
		if !r.RecordedAt.After(rx.IssuedAt) {
			continue
		}
		if match(rx, r) {
			rep.RetrospectiveFindings = append(rep.RetrospectiveFindings, Finding{Rule: r})
		}
	}
	rep.AtRisk = len(rep.RetrospectiveFindings) > 0
	return rep
}

// WorstSeverity 返回命中中的最高严重级别，无命中时返回 0。
func WorstSeverity(findings []Finding) Severity {
	worst := Severity(0)
	for _, f := range findings {
		if f.Rule.Severity > worst {
			worst = f.Rule.Severity
		}
	}
	return worst
}

// SortFindings 按严重级别降序、指南版本升序排序，便于报告展示。
func SortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Rule.Severity != findings[j].Rule.Severity {
			return findings[i].Rule.Severity > findings[j].Rule.Severity
		}
		return findings[i].Rule.GuidelineVersion < findings[j].Rule.GuidelineVersion
	})
}
