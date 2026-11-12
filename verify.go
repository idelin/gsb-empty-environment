package netsec

import (
	"fmt"
	"strings"
)

// Decision reasons returned by Verify.
const (
	ReasonNoMetadata           = "no-metadata"
	ReasonUnknownComponent     = "unknown-component"
	ReasonUnknownArtifact      = "unknown-artifact"
	ReasonChecksumMismatch     = "checksum-mismatch"
	ReasonNoUsableChecksum     = "no-usable-checksum"
	ReasonUnsupportedAlgorithm = "unsupported-algorithm"
	ReasonChecksumMatched      = "checksum-matched"
	ReasonTrustedArtifact      = "trusted-artifact"
)

// Finding IDs used by the verification decision path.
const (
	FindingChecksumMismatch    = "checksum-mismatch"
	FindingNoUsableChecksum    = "no-usable-checksum"
	FindingTrustedArtifactRule = "trusted-artifact-rule"
)

// Verify decides whether the artifact identified by ref with the observed
// checksums is allowed. observed maps lower-cased algorithm names to the
// digest computed from the downloaded bytes.
//
// Primary component checksums may use the well-known digest algorithms.
// also-trust fallback checksums are honored only for algorithms that appear in
// an also-trust entry of the parsed metadata. If no checksum matches, a
// trusted-artifacts rule may still accept the coordinate; risks attached to
// the matching rule are included in the decision.
func (m *Metadata) Verify(ref ArtifactRef, observed map[string]string) Decision {
	if m == nil {
		return Decision{Allowed: false, Reason: ReasonNoMetadata}
	}
	findings := m.Analyze(nil)

	component := m.findComponent(ref.Group, ref.Name, ref.Version)
	if component == nil {
		return m.denyOrTrust(ref, ReasonUnknownComponent, findings)
	}
	artifact := m.findArtifact(component, ref.File)
	if artifact == nil {
		return m.denyOrTrust(ref, ReasonUnknownArtifact, findings)
	}

	checksumDecision := verifyChecksums(ref, artifact.Checksums, observed, m.allowedAlsoTrust())
	findings = append(findings, checksumDecision.Findings...)
	if checksumDecision.Allowed {
		return Decision{Allowed: true, Reason: ReasonChecksumMatched, Findings: findings}
	}
	return m.denyOrTrust(ref, checksumDecision.Reason, findings)
}

func (m *Metadata) allowedAlsoTrust() map[string]bool {
	allowed := make(map[string]bool, len(m.AlsoTrust))
	for _, algorithm := range m.AlsoTrust {
		allowed[algorithm] = true
	}
	return allowed
}

func (m *Metadata) denyOrTrust(ref ArtifactRef, denyReason string, findings []Finding) Decision {
	trusted := m.matchingTrustFindings(ref)
	if len(trusted) > 0 {
		findings = append(findings, trusted...)
		return Decision{Allowed: true, Reason: ReasonTrustedArtifact, Findings: findings}
	}
	return Decision{Allowed: false, Reason: denyReason, Findings: findings}
}

func (m *Metadata) matchingTrustFindings(ref ArtifactRef) []Finding {
	var findings []Finding
	for index := range m.TrustRules {
		rule := &m.TrustRules[index]
		matched, err := rule.Matches(ref.Group, ref.Name, ref.Version, ref.File)
		if err != nil || !matched {
			continue
		}
		findings = append(findings, Finding{
			ID:       FindingTrustedArtifactRule,
			Severity: SeverityRisk,
			Message: fmt.Sprintf(
				"artifact %q accepted by a trusted-artifacts rule without a verified checksum", ref.File),
			Rule: rule,
		})
		findings = append(findings, analyzeRuleRegex(rule)...)
	}
	return findings
}

func verifyChecksums(ref ArtifactRef, expected []Checksum, observed map[string]string, alsoTrustAllowed map[string]bool) Decision {
	matched := false
	usable := false
	var unsupported []string
	var mismatched []string

	for _, checksum := range expected {
		actual, present := observed[checksum.Algorithm]
		if !present {
			continue
		}
		if checksum.AlsoTrust && !alsoTrustAllowed[checksum.Algorithm] {
			unsupported = append(unsupported, checksum.Algorithm)
			continue
		}
		if !isKnownAlgorithm(checksum.Algorithm) {
			unsupported = append(unsupported, checksum.Algorithm)
			continue
		}
		usable = true
		if equalDigest(actual, checksum.Value) {
			matched = true
			continue
		}
		mismatched = append(mismatched, checksum.Algorithm)
	}

	switch {
	case matched:
		return Decision{Allowed: true, Reason: ReasonChecksumMatched}
	case len(unsupported) > 0 && !usable:
		return Decision{
			Allowed: false,
			Reason:  ReasonUnsupportedAlgorithm,
			Findings: []Finding{{
				ID:       FindingUnknownChecksumAlgo,
				Severity: SeverityWarning,
				Message: fmt.Sprintf(
					"artifact %q only provides unsupported or unlisted also-trust algorithms: %s",
					ref.File, strings.Join(unsupported, ", ")),
			}},
		}
	case len(mismatched) > 0:
		return Decision{
			Allowed: false,
			Reason:  ReasonChecksumMismatch,
			Findings: []Finding{{
				ID:       FindingChecksumMismatch,
				Severity: SeverityRisk,
				Message: fmt.Sprintf(
					"artifact %q failed checksum verification for: %s",
					ref.File, strings.Join(mismatched, ", ")),
			}},
		}
	default:
		return Decision{
			Allowed: false,
			Reason:  ReasonNoUsableChecksum,
			Findings: []Finding{{
				ID:       FindingNoUsableChecksum,
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("artifact %q has no checksum verifiable against observed data", ref.File),
			}},
		}
	}
}

func (m *Metadata) findComponent(group, name, version string) *Component {
	for index := range m.Components {
		component := &m.Components[index]
		if component.Group == group && component.Name == name && component.Version == version {
			return component
		}
	}
	return nil
}

func (m *Metadata) findArtifact(component *Component, file string) *Artifact {
	for index := range component.Artifacts {
		artifact := &component.Artifacts[index]
		if artifact.Name == "" || artifact.Name == file {
			return artifact
		}
	}
	return nil
}

func equalDigest(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
