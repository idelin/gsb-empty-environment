package flowspec

import (
	"fmt"
	"log"
)

// AlarmKind classifies why a FlowSpec rule was rejected.
type AlarmKind string

const (
	// AlarmMalformedNLRI indicates the NLRI is malformed, e.g. its
	// components are not in ascending type order (RFC 8955, Section 4.2).
	AlarmMalformedNLRI AlarmKind = "malformed-nlri"
	// AlarmNoUnicastRoute indicates no unicast route covers the rule's
	// destination prefix at all.
	AlarmNoUnicastRoute AlarmKind = "no-unicast-route"
	// AlarmOriginMismatch indicates the best-match unicast route for the
	// destination prefix was received from a different neighboring AS
	// than the FlowSpec rule (RFC 8955, Section 6, rule a).
	AlarmOriginMismatch AlarmKind = "origin-mismatch"
	// AlarmForeignMoreSpecific indicates a more-specific unicast route
	// inside the destination prefix was received from a different
	// neighboring AS (RFC 8955, Section 6, rule b).
	AlarmForeignMoreSpecific AlarmKind = "foreign-more-specific"
)

// Alarm is raised whenever a FlowSpec rule fails validation. The rule is
// never installed when an alarm is raised.
type Alarm struct {
	Kind   AlarmKind
	Rule   Rule
	Detail string
}

// Alerter receives validation alarms. Implementations must be safe for
// concurrent use if the Validator is used concurrently.
type Alerter interface {
	Alert(Alarm)
}

// LogAlerter is an Alerter that logs alarms through a log.Logger.
type LogAlerter struct {
	Logger *log.Logger
}

// Alert implements Alerter.
func (a LogAlerter) Alert(al Alarm) {
	a.Logger.Printf("flowspec: rule rejected (%s): %s; originator AS %d, NLRI %d components",
		al.Kind, al.Detail, al.Rule.OriginatorAS, len(al.Rule.NLRI.Components))
}

// Rule is a Flow Specification rule received from a BGP neighbor.
// OriginatorAS is the neighboring AS that advertised the rule.
type Rule struct {
	OriginatorAS uint32
	NLRI         NLRI
}

// Result is the outcome of validating a rule.
type Result struct {
	// Installed is true only when the rule passed validation and may be
	// installed into the forwarding plane.
	Installed bool
	// Reason is a human-readable explanation of the outcome.
	Reason string
}

// Validator validates FlowSpec rules against the local unicast RIB
// following RFC 8955, Section 6.
type Validator struct {
	RIB     UnicastRIB
	Alerter Alerter
}

// Validate checks a rule and reports whether it may be installed. Every
// rejection raises exactly one alarm through the configured Alerter.
func (v *Validator) Validate(rule Rule) Result {
	if err := rule.NLRI.ValidateOrder(); err != nil {
		return v.reject(rule, AlarmMalformedNLRI, err.Error())
	}
	dest, err := rule.NLRI.DestPrefix()
	if err != nil {
		return v.reject(rule, AlarmMalformedNLRI, err.Error())
	}
	best, ok := v.RIB.BestMatch(dest)
	if !ok {
		return v.reject(rule, AlarmNoUnicastRoute,
			fmt.Sprintf("no unicast route covers destination prefix %s", dest))
	}
	if best.NeighborAS != rule.OriginatorAS {
		return v.reject(rule, AlarmOriginMismatch,
			fmt.Sprintf("best unicast route %s for %s originated from AS %d, rule from AS %d",
				best.Prefix, dest, best.NeighborAS, rule.OriginatorAS))
	}
	for _, r := range v.RIB.MoreSpecifics(dest) {
		if r.NeighborAS != rule.OriginatorAS {
			return v.reject(rule, AlarmForeignMoreSpecific,
				fmt.Sprintf("more-specific unicast route %s inside %s originated from AS %d, rule from AS %d",
					r.Prefix, dest, r.NeighborAS, rule.OriginatorAS))
		}
	}
	return Result{Installed: true, Reason: "validated per RFC 8955 section 6"}
}

func (v *Validator) reject(rule Rule, kind AlarmKind, detail string) Result {
	if v.Alerter != nil {
		v.Alerter.Alert(Alarm{Kind: kind, Rule: rule, Detail: detail})
	}
	return Result{Installed: false, Reason: detail}
}
