package netsec

import "fmt"

// Analyze cross-checks parsed metadata against the lockfile and returns every
// risk or bypass finding. A nil lockfile disables lockfile coverage checks.
func (m *Metadata) Analyze(lock *Lockfile) []Finding {
	var findings []Finding

	for ruleIndex := range m.TrustRules {
		rule := &m.TrustRules[ruleIndex]
		findings = append(findings, analyzeRuleRegex(rule)...)
		findings = append(findings, analyzeRuleCoverage(rule, lock)...)
	}

	for _, component := range m.Components {
		for _, checksum := range componentChecksums(component) {
			if !isKnownAlgorithm(checksum.Algorithm) {
				findings = append(findings, Finding{
					ID:       FindingUnknownChecksumAlgo,
					Severity: SeverityWarning,
					Message: fmt.Sprintf(
						"component %s:%s:%s uses unsupported checksum algorithm %q",
						component.Group, component.Name, component.Version, checksum.Algorithm),
				})
			}
		}
		if lock != nil && !lock.Contains(component.Group, component.Name, component.Version) {
			findings = append(findings, Finding{
				ID:       FindingVersionNotInLockfile,
				Severity: SeverityWarning,
				Message: fmt.Sprintf(
					"parsed component %s:%s:%s is not covered by the lockfile; dynamic resolution could bypass pinning",
					component.Group, component.Name, component.Version),
			})
		}
	}

	return findings
}

func analyzeRuleRegex(rule *TrustRule) []Finding {
	if !rule.Regex {
		return nil
	}
	var findings []Finding
	regexFields := []struct {
		name    string
		pattern *string
	}{
		{"group", rule.Group},
		{"name", rule.Name},
		{"version", rule.Version},
		{"file", rule.File},
	}
	for _, field := range regexFields {
		if field.pattern == nil {
			continue
		}
		pattern := *field.pattern
		if _, err := compileFullPattern(pattern); err != nil {
			findings = append(findings, Finding{
				ID:       FindingInvalidRegex,
				Severity: SeverityRisk,
				Message:  fmt.Sprintf("trusted-artifacts %s regex %q does not compile: %v", field.name, pattern, err),
				Rule:     rule,
			})
			continue
		}
		if IsMatchAllRegex(pattern) {
			findings = append(findings, Finding{
				ID:       FindingOverbroadRegex,
				Severity: SeverityRisk,
				Message: fmt.Sprintf(
					"trusted-artifacts %s regex %q matches every value and trusts arbitrary artifacts",
					field.name, pattern),
				Rule: rule,
			})
		}
		if HasUnescapedDot(pattern) {
			findings = append(findings, Finding{
				ID:       FindingUnescapedDot,
				Severity: SeverityRisk,
				Message: fmt.Sprintf(
					"trusted-artifacts %s regex %q contains an unescaped dot; any character is accepted at that position",
					field.name, pattern),
				Rule: rule,
			})
		}
	}
	return findings
}

func analyzeRuleCoverage(rule *TrustRule, lock *Lockfile) []Finding {
	if lock == nil || rule.Version == nil {
		return nil
	}
	version := *rule.Version
	dynamic := rule.Regex || IsDynamicVersion(version)
	if !dynamic {
		return nil
	}
	matched := lock.MatchesRule(*rule)
	if matched {
		return nil
	}
	id := FindingUncoveredTrustRule
	message := "trusted-artifacts rule is not covered by any lockfile entry"
	if IsDynamicVersion(version) {
		id = FindingUncoveredDynamicVersion
		message = fmt.Sprintf(
			"dynamic version selector %q is not covered by any lockfile entry; resolution can drift outside the lockfile",
			version)
	}
	return []Finding{{
		ID:       id,
		Severity: SeverityWarning,
		Message:  message,
		Rule:     rule,
	}}
}

// Contains reports whether the lockfile pins an exact coordinate.
func (l *Lockfile) Contains(group, name, version string) bool {
	for _, entry := range l.Entries {
		if entry.Group == group && entry.Name == name && entry.Version == version {
			return true
		}
	}
	return false
}

// MatchesRule reports whether at least one lockfile coordinate satisfies the
// rule. Regex rules evaluate with whole-string semantics and dynamic version
// selectors use Gradle selector semantics.
func (l *Lockfile) MatchesRule(rule TrustRule) bool {
	for _, entry := range l.Entries {
		matched, err := rule.Matches(entry.Group, entry.Name, entry.Version, "")
		if err != nil {
			return false
		}
		if matched {
			return true
		}
	}
	return false
}

func componentChecksums(component Component) []Checksum {
	var checksums []Checksum
	for _, artifact := range component.Artifacts {
		checksums = append(checksums, artifact.Checksums...)
	}
	return checksums
}

func isKnownAlgorithm(algorithm string) bool {
	return primaryChecksums[algorithm]
}
