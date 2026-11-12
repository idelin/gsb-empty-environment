package vex

import (
	"time"
)

// State is the CycloneDX vulnerability analysis state. Only the five states
// defined by the CycloneDX VEX specification are recognized.
type State string

const (
	StateExploitable   State = "exploitable"
	StateInTriage      State = "in_triage"
	StateFalsePositive State = "false_positive"
	StateNotAffected   State = "not_affected"
	StateResolved      State = "resolved"
)

var validStates = map[State]bool{
	StateExploitable:   true,
	StateInTriage:      true,
	StateFalsePositive: true,
	StateNotAffected:   true,
	StateResolved:      true,
}

// Justification is the CycloneDX impact-analysis justification that must
// accompany a not_affected statement.
type Justification string

const (
	JustificationCodeNotPresent               Justification = "code_not_present"
	JustificationCodeNotReachable             Justification = "code_not_reachable"
	JustificationRequiresConfiguration        Justification = "requires_configuration"
	JustificationRequiresEnvironment          Justification = "requires_environment"
	JustificationProtectedByMitigatingControl Justification = "protected_by_mitigating_control"
	JustificationTestedNotVulnerable          Justification = "tested_not_vulnerable"
)

var validJustifications = map[Justification]bool{
	JustificationCodeNotPresent:               true,
	JustificationCodeNotReachable:             true,
	JustificationRequiresConfiguration:        true,
	JustificationRequiresEnvironment:          true,
	JustificationProtectedByMitigatingControl: true,
	JustificationTestedNotVulnerable:          true,
}

// Component is a bill-of-materials component against which a VEX statement
// may be adopted. Supplier names the trusted-issuer set to consult; ReleasedAt
// is the publication time of the shipped version.
type Component struct {
	BOMRef     string
	PURL       string
	Supplier   string
	Version    string
	ReleasedAt time.Time
}

// Affect is one entry of a statement's affects list. The bom-ref anchors the
// target component while PURLRange constrains the affected versions, using a
// concrete purl ("pkg:maven/...@1.2.3") or an expression with a vers-style or
// interval version range ("pkg:...@vers:semver/>=1.0|<2.0").
type Affect struct {
	BOMRef    string
	PURLRange string
}

// Statement is a vendor VEX claim about one vulnerability.
type Statement struct {
	VulnerabilityID string
	State           State
	Justification   Justification
	Affects         []Affect
	Issuer          string
	IssuedAt        time.Time
}

// Alert is an aggregated vulnerability finding awaiting suppression
// decisions.
type Alert struct {
	VulnerabilityID string
	BOMRef          string
}

// RejectReason explains why a statement failed adoption. Rejections are
// fail-closed: any reason leaves the original alert untouched.
type RejectReason string

const (
	ReasonUnknownState         RejectReason = "unknown_state"
	ReasonMissingJustification RejectReason = "missing_justification"
	ReasonUnknownJustification RejectReason = "unknown_justification"
	ReasonIssuerNotTrusted     RejectReason = "issuer_not_trusted"
	ReasonStaleTimestamp       RejectReason = "stale_timestamp"
	ReasonMissingTimestamp     RejectReason = "missing_timestamp"
	ReasonBOMRefMismatch       RejectReason = "bom_ref_mismatch"
	ReasonPURLMismatch         RejectReason = "purl_mismatch"
	ReasonVersionOutsideRange  RejectReason = "version_outside_range"
	ReasonMalformedPURL        RejectReason = "malformed_purl"
	ReasonNoAffectsMatch       RejectReason = "no_affects_match"
)

// Decision is the result of evaluating one statement against one component.
type Decision struct {
	Statement     Statement
	Component     Component
	Adopted       bool
	Suppresses    bool
	RejectReasons []RejectReason
}

// TrustPolicy maps each component supplier to the issuers allowed to speak
// for that supplier's components.
type TrustPolicy struct {
	TrustedIssuers map[string]map[string]bool
}

// NewTrustPolicy builds a TrustPolicy from supplier to trusted issuer lists.
func NewTrustPolicy(bySupplier map[string][]string) TrustPolicy {
	out := TrustPolicy{TrustedIssuers: make(map[string]map[string]bool, len(bySupplier))}
	for supplier, issuers := range bySupplier {
		set := make(map[string]bool, len(issuers))
		for _, issuer := range issuers {
			set[issuer] = true
		}
		out.TrustedIssuers[supplier] = set
	}
	return out
}

// IsTrusted reports whether issuer may issue VEX statements for supplier.
func (p TrustPolicy) IsTrusted(supplier, issuer string) bool {
	set, ok := p.TrustedIssuers[supplier]
	if !ok {
		return false
	}
	return set[issuer]
}

// Engine applies the VEX adoption policy for the vulnerability gate.
type Engine struct {
	Policy TrustPolicy
}

// Adopt evaluates a single statement against a component. A statement is
// adopted only when every trust condition holds; all violated conditions are
// recorded rather than returning on the first failure.
func (e Engine) Adopt(stmt Statement, comp Component) Decision {
	d := Decision{Statement: stmt, Component: comp}

	if !validStates[stmt.State] {
		d.RejectReasons = append(d.RejectReasons, ReasonUnknownState)
	}

	if stmt.State == StateNotAffected {
		switch {
		case stmt.Justification == "":
			d.RejectReasons = append(d.RejectReasons, ReasonMissingJustification)
		case !validJustifications[stmt.Justification]:
			d.RejectReasons = append(d.RejectReasons, ReasonUnknownJustification)
		}
	}

	if !e.Policy.IsTrusted(comp.Supplier, stmt.Issuer) {
		d.RejectReasons = append(d.RejectReasons, ReasonIssuerNotTrusted)
	}

	if stmt.IssuedAt.IsZero() {
		d.RejectReasons = append(d.RejectReasons, ReasonMissingTimestamp)
	} else if stmt.IssuedAt.Before(comp.ReleasedAt) {
		d.RejectReasons = append(d.RejectReasons, ReasonStaleTimestamp)
	}

	d.RejectReasons = append(d.RejectReasons, e.matchAffects(stmt, comp)...)

	d.Adopted = len(d.RejectReasons) == 0
	// Only an adopted not_affected claim suppresses; adopted exploitable,
	// in_triage, false_positive and resolved claims never silence an alert.
	d.Suppresses = d.Adopted && stmt.State == StateNotAffected
	return d
}

// matchAffects requires at least one affects entry whose bom-ref equals the
// component bom-ref and whose purl version range contains the component
// version. Both conditions must hold on the same entry.
func (e Engine) matchAffects(stmt Statement, comp Component) []RejectReason {
	componentPURL, err := parsePurl(comp.PURL)
	if err != nil {
		return []RejectReason{ReasonMalformedPURL}
	}

	sawBOMRef := false
	sawPURL := false
	sawRange := false
	for _, affect := range stmt.Affects {
		if affect.BOMRef != comp.BOMRef {
			continue
		}
		sawBOMRef = true

		affectPURL, err := parsePurl(affect.PURLRange)
		if err != nil {
			continue
		}
		if !affectPURL.sameComponent(componentPURL) {
			continue
		}
		sawPURL = true

		rangeExpr := affectPURL.Version
		if rangeExpr == "" || rangeContains(rangeExpr, comp.Version) {
			sawRange = true
		}
	}

	if sawRange {
		return nil
	}
	if !sawBOMRef {
		return []RejectReason{ReasonNoAffectsMatch, ReasonBOMRefMismatch}
	}
	if !sawPURL {
		return []RejectReason{ReasonPURLMismatch}
	}
	return []RejectReason{ReasonVersionOutsideRange}
}

// AggregationResult classifies one alert after VEX evaluation.
type AggregationResult struct {
	Alert      Alert
	Suppressed bool
	Decisions  []Decision
}

// Apply evaluates every statement against every alert. Components are looked
// up by bom-ref; an alert whose component is missing cannot be suppressed.
// Only an adopted not_affected statement suppresses, and all other states and
// rejected statements leave the alert retained.
func (e Engine) Apply(alerts []Alert, components []Component, statements []Statement) []AggregationResult {
	byBOMRef := make(map[string]Component, len(components))
	for _, comp := range components {
		byBOMRef[comp.BOMRef] = comp
	}

	results := make([]AggregationResult, 0, len(alerts))
	for _, alert := range alerts {
		res := AggregationResult{Alert: alert}
		comp, ok := byBOMRef[alert.BOMRef]
		for _, stmt := range statements {
			if stmt.VulnerabilityID != alert.VulnerabilityID {
				continue
			}
			if !ok {
				res.Decisions = append(res.Decisions, Decision{
					Statement:     stmt,
					Adopted:       false,
					Suppresses:    false,
					RejectReasons: []RejectReason{ReasonNoAffectsMatch, ReasonBOMRefMismatch},
				})
				continue
			}
			decision := e.Adopt(stmt, comp)
			res.Decisions = append(res.Decisions, decision)
			if decision.Suppresses {
				res.Suppressed = true
			}
		}
		results = append(results, res)
	}
	return results
}

// Retained returns the alerts that were not suppressed, preserving order.
func Retained(results []AggregationResult) []Alert {
	var out []Alert
	for _, res := range results {
		if !res.Suppressed {
			out = append(out, res.Alert)
		}
	}
	return out
}
