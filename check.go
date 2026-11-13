package netsec

import "fmt"

type FindingKind = string

const (
	KindSensitiveLevel FindingKind = "sensitive-level"
	KindShadowedRule   FindingKind = "shadowed-rule"
	KindMissingDefault FindingKind = "missing-default"
)

const (
	FindingSensitiveLevel = KindSensitiveLevel
	FindingShadowedRule   = KindShadowedRule
	FindingMissingDefault = KindMissingDefault

	FindingSensitiveResource = KindSensitiveLevel
	FindingShadowed          = KindShadowedRule
	FindingNoDefault         = KindMissingDefault

	KindSensitiveResource = KindSensitiveLevel
	KindShadowed          = KindShadowedRule
	KindNoDefault         = KindMissingDefault
)

type Finding struct {
	Kind         FindingKind `json:"kind"`
	Code         FindingKind `json:"code"`
	RuleIndex    int         `json:"ruleIndex"`
	ProbeIndex   int         `json:"probeIndex"`
	RequestIndex int         `json:"requestIndex"`
	Level        Level       `json:"level,omitempty"`
	Resource     string      `json:"resource,omitempty"`
	Message      string      `json:"message"`
}

type Issue = Finding
type Findings = []Finding

type Report struct {
	Findings               []Finding `json:"findings"`
	Issues                 []Finding `json:"issues"`
	SensitiveLevelFindings []Finding `json:"sensitiveLevelFindings"`
	ShadowedRuleFindings   []Finding `json:"shadowedRuleFindings"`
	MissingDefaultFindings []Finding `json:"missingDefaultFindings"`
	SensitiveIssues        []Finding `json:"sensitiveIssues"`
	ShadowedRules          []Finding `json:"shadowedRules"`
	MissingDefaultIssues   []Finding `json:"missingDefaultIssues"`
}

func (report Report) OK() bool {
	return len(report.Findings) == 0
}

func Check(rules []Rule, probes []Probe) []Finding {
	var sensitiveFindings []Finding
	var missingDefaultFindings []Finding
	var shadowedFindings []Finding

	for probeIndex, probe := range probes {
		level, ruleIndex := Resolve(rules, probe)
		request := normalizeProbe(probe)

		if isSensitiveProbe(probe) && isLoggedRequestLevel(level) {
			sensitiveFindings = append(sensitiveFindings, Finding{
				Kind:         KindSensitiveLevel,
				Code:         KindSensitiveLevel,
				RuleIndex:    ruleIndex,
				ProbeIndex:   probeIndex,
				RequestIndex: probeIndex,
				Level:        level,
				Resource:     request.resource,
				Message: fmt.Sprintf(
					"sensitive resource %q resolves to audit level %q",
					request.resource,
					level,
				),
			})
		}

		if ruleIndex < 0 {
			missingDefaultFindings = append(missingDefaultFindings, Finding{
				Kind:         KindMissingDefault,
				Code:         KindMissingDefault,
				RuleIndex:    -1,
				ProbeIndex:   probeIndex,
				RequestIndex: probeIndex,
				Level:        LevelNone,
				Resource:     request.resource,
				Message:      missingDefaultMessage(probeIndex, request),
			})
		}
	}

	for ruleIndex := 1; ruleIndex < len(rules); ruleIndex++ {
		if ruleIsShadowed(rules, ruleIndex) {
			shadowedFindings = append(shadowedFindings, Finding{
				Kind:         KindShadowedRule,
				Code:         KindShadowedRule,
				RuleIndex:    ruleIndex,
				ProbeIndex:   -1,
				RequestIndex: -1,
				Message:      fmt.Sprintf("audit rule %d is fully shadowed by earlier rules", ruleIndex),
			})
		}
	}

	findings := make([]Finding, 0, len(sensitiveFindings)+len(shadowedFindings)+len(missingDefaultFindings))
	findings = append(findings, sensitiveFindings...)
	findings = append(findings, shadowedFindings...)
	findings = append(findings, missingDefaultFindings...)
	return findings
}

func Validate(rules []Rule, probes []Probe) []Finding {
	return Check(rules, probes)
}

func CheckAuditPolicy(rules []Rule, probes []Probe) []Finding {
	return Check(rules, probes)
}

func CheckPolicy(rules []Rule, probes []Probe) []Finding {
	return Check(rules, probes)
}

func Analyze(rules []Rule, probes []Probe) Report {
	findings := Check(rules, probes)
	report := Report{Findings: findings, Issues: findings}
	for _, finding := range findings {
		switch finding.Kind {
		case KindSensitiveLevel:
			report.SensitiveLevelFindings = append(report.SensitiveLevelFindings, finding)
			report.SensitiveIssues = append(report.SensitiveIssues, finding)
		case KindShadowedRule:
			report.ShadowedRuleFindings = append(report.ShadowedRuleFindings, finding)
			report.ShadowedRules = append(report.ShadowedRules, finding)
		case KindMissingDefault:
			report.MissingDefaultFindings = append(report.MissingDefaultFindings, finding)
			report.MissingDefaultIssues = append(report.MissingDefaultIssues, finding)
		}
	}
	return report
}

func isLoggedRequestLevel(level Level) bool {
	return level == LevelRequest || level == LevelRequestResponse
}

func missingDefaultMessage(probeIndex int, request normalizedProbe) string {
	resource := request.resource
	if resource == "" {
		resource = request.nonResourceURL
	}
	return fmt.Sprintf("probe %d matches no audit rule (resource %q); a default rule is missing", probeIndex, resource)
}
