// Package rxcheck 实现多版本医疗处方配伍禁忌的双时态判定。
//
// 双时态模型：
//   - 业务时间（valid time）：处方实际开立的时间。
//   - 知悉时间（transaction time）：配伍禁忌规则被规则库收录/知悉的时间，
//     即临床指南版本生效进入系统的时间。
//
// 系统支持两类判定：
//   - 开方合规性判定：以处方开立时刻规则库"当时知悉"的规则版本为准。
//   - 追溯风险审计：以查询时刻规则库"当前最新"的规则版本重新评估历史处方，
//     若某药品组合的严重级别较开立时上升，则标记追溯风险。
package rxcheck

import (
	"fmt"
	"sort"
	"strings"
)

// Severity 表示配伍禁忌的严重级别，数值越大越严重。
type Severity int

const (
	SeverityNone            Severity = iota // 无禁忌
	SeverityCaution                         // 慎用
	SeverityContraindicated                 // 禁用
)

func (s Severity) String() string {
	switch s {
	case SeverityCaution:
		return "慎用"
	case SeverityContraindicated:
		return "禁用"
	default:
		return "无禁忌"
	}
}

// Rule 是一条配伍禁忌规则，针对一对药品，自 KnownFrom（知悉时间）起被规则库知悉。
// 同一对药品可存在多条规则，分别来自不同版本的临床指南。
type Rule struct {
	DrugA     string   // 药品甲（无序，与 DrugB 构成药品对）
	DrugB     string   // 药品乙
	Severity  Severity // 禁忌级别
	Guideline string   // 来源临床指南版本
	KnownFrom int      // 知悉时间：该规则被规则库收录的时间
}

// pairKey 生成与药品顺序无关的药品对键。
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

// RuleStore 是支持多版本的配伍禁忌规则库。
type RuleStore struct {
	rules []Rule
}

// NewRuleStore 创建空规则库。
func NewRuleStore() *RuleStore { return &RuleStore{} }

// AddRule 收录一条规则。规则一旦收录不可修改（双时态只追加）。
func (s *RuleStore) AddRule(r Rule) {
	s.rules = append(s.rules, r)
}

// ruleAsOf 返回药品对在知悉时间 knownAt 时生效的规则：
// 即 KnownFrom <= knownAt 中 KnownFrom 最大的一条；无则返回 nil。
func (s *RuleStore) ruleAsOf(drugA, drugB string, knownAt int) *Rule {
	key := pairKey(drugA, drugB)
	var best *Rule
	for i := range s.rules {
		r := &s.rules[i]
		if pairKey(r.DrugA, r.DrugB) == key && r.KnownFrom <= knownAt {
			if best == nil || r.KnownFrom > best.KnownFrom {
				best = r
			}
		}
	}
	return best
}

// SeverityAsOf 返回药品对在指定知悉时间下的禁忌级别（无规则则为 SeverityNone）。
func (s *RuleStore) SeverityAsOf(drugA, drugB string, knownAt int) Severity {
	if r := s.ruleAsOf(drugA, drugB, knownAt); r != nil {
		return r.Severity
	}
	return SeverityNone
}

// Prescription 是一张历史处方。
type Prescription struct {
	ID           string   // 处方编号
	Drugs        []string // 药品列表
	BusinessTime int      // 业务时间：处方开立时间
}

// drugPairs 枚举处方中所有两两药品组合（去重、排序后确定性输出）。
func (p Prescription) drugPairs() [][2]string {
	seen := map[string]bool{}
	var pairs [][2]string
	for i := 0; i < len(p.Drugs); i++ {
		for j := i + 1; j < len(p.Drugs); j++ {
			a, b := p.Drugs[i], p.Drugs[j]
			if a == b {
				continue
			}
			key := pairKey(a, b)
			if seen[key] {
				continue
			}
			seen[key] = true
			if a > b {
				a, b = b, a
			}
			pairs = append(pairs, [2]string{a, b})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	return pairs
}

// Finding 描述一处配伍发现。
type Finding struct {
	DrugA     string
	DrugB     string
	Severity  Severity // 评估所依据规则下的级别
	Guideline string   // 评估所依据的指南版本（无规则时为空）
}

func (f Finding) String() string {
	return fmt.Sprintf("%s + %s：%s（依据指南 %s）", f.DrugA, f.DrugB, f.Severity, f.Guideline)
}

// ComplianceReport 是开方合规性判定报告。
type ComplianceReport struct {
	PrescriptionID string
	EvaluatedAt    int // 评估所用的知悉时间（即处方开立的业务时间）
	Compliant      bool
	Violations     []Finding // 级别 >= 慎用 的组合
}

// CheckCompliance 判定处方在其开立时刻是否合规：
// 使用知悉时间 = 处方业务时间 的规则版本进行评估。
func CheckCompliance(store *RuleStore, p Prescription) ComplianceReport {
	rep := ComplianceReport{
		PrescriptionID: p.ID,
		EvaluatedAt:    p.BusinessTime,
		Compliant:      true,
	}
	for _, pair := range p.drugPairs() {
		if r := store.ruleAsOf(pair[0], pair[1], p.BusinessTime); r != nil && r.Severity >= SeverityCaution {
			rep.Compliant = false
			rep.Violations = append(rep.Violations, Finding{
				DrugA: pair[0], DrugB: pair[1],
				Severity: r.Severity, Guideline: r.Guideline,
			})
		}
	}
	return rep
}

// RiskReport 是追溯风险审计报告。
type RiskReport struct {
	PrescriptionID       string
	EvaluatedAt          int       // 评估所用的知悉时间（查询时刻）
	HasRetrospectiveRisk bool      // 是否存在追溯风险
	Findings             []Finding // 当前规则下级别 >= 慎用 的组合
}

// AuditRisk 以查询时刻 queryTime 知悉的最新规则重新评估历史处方，
// 识别开立时合规、但按当前医学规则存在风险的药品组合（追溯风险）。
func AuditRisk(store *RuleStore, p Prescription, queryTime int) RiskReport {
	rep := RiskReport{PrescriptionID: p.ID, EvaluatedAt: queryTime}
	for _, pair := range p.drugPairs() {
		r := store.ruleAsOf(pair[0], pair[1], queryTime)
		if r == nil || r.Severity < SeverityCaution {
			continue
		}
		rep.Findings = append(rep.Findings, Finding{
			DrugA: pair[0], DrugB: pair[1],
			Severity: r.Severity, Guideline: r.Guideline,
		})
		// 追溯风险：当前级别高于处方开立时刻的级别。
		if r.Severity > store.SeverityAsOf(pair[0], pair[1], p.BusinessTime) {
			rep.HasRetrospectiveRisk = true
		}
	}
	return rep
}

// FormatCompliance 生成合规性报告文本。
func FormatCompliance(rep ComplianceReport) string {
	var b strings.Builder
	verdict := "合格"
	if !rep.Compliant {
		verdict = "不合格"
	}
	fmt.Fprintf(&b, "处方 %s 开方合规性判定（按知悉时间 %d 的规则版本）：%s\n",
		rep.PrescriptionID, rep.EvaluatedAt, verdict)
	for _, v := range rep.Violations {
		fmt.Fprintf(&b, "  违规：%s\n", v)
	}
	return b.String()
}

// FormatRisk 生成追溯风险审计报告文本。
func FormatRisk(rep RiskReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "处方 %s 追溯风险审计（按知悉时间 %d 的最新规则版本）：\n",
		rep.PrescriptionID, rep.EvaluatedAt)
	if !rep.HasRetrospectiveRisk {
		b.WriteString("  无追溯风险\n")
	}
	for _, f := range rep.Findings {
		fmt.Fprintf(&b, "  追溯风险提示：%s\n", f)
	}
	return b.String()
}
