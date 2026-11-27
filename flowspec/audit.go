package flowspec

import (
	"fmt"
	"net"
	"time"
)

// Severity classifies a security finding.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// FindingCode is a stable, machine-readable conflict/admission code.
type FindingCode string

const (
	// CodeCrossTenantBlackhole: an origin not authorized by ROA announces a
	// traffic-rate=0 blackhole for another tenant's prefix.
	CodeCrossTenantBlackhole FindingCode = "cross-tenant-blackhole-hijack"
	// CodeUnauthorizedBlackhole: a rate=0 rule fails ROA admission (not found /
	// bad length) without a distinct authorized origin.
	CodeUnauthorizedBlackhole FindingCode = "unauthorized-blackhole"
	CodeROAOriginMismatch     FindingCode = "roa-origin-mismatch"
	CodeROANotFound           FindingCode = "roa-not-found"
	CodePrefixTooWide         FindingCode = "prefix-too-wide"
	CodePrefixTooLong         FindingCode = "prefix-too-long"
	CodeROAExpired            FindingCode = "roa-expired"
	CodeMissingDestPrefix     FindingCode = "missing-destination-prefix"

	// CodeShadowedPermit: a higher-priority drop fully covers a lower permit.
	CodeShadowedPermit FindingCode = "shadowed-permit"
	// CodePartiallyShadowedPermit: a higher drop overlaps (but does not fully
	// cover) a lower permit, or coverage is not statically decidable.
	CodePartiallyShadowedPermit FindingCode = "partially-shadowed-permit"
	// CodePolicyConflict: equal-specificity rules assert contradictory verdicts.
	CodePolicyConflict FindingCode = "equal-priority-policy-conflict"
	// CodeRedirectConflict: overlapping rules redirect to different targets.
	CodeRedirectConflict FindingCode = "redirect-target-conflict"
	// CodeCrossTenantRedirect: redirect-to-IP targets an address owned by
	// another tenant.
	CodeCrossTenantRedirect FindingCode = "cross-tenant-redirect-hijack"
)

// Finding is one deterministic audit result.
type Finding struct {
	Code       FindingCode `json:"code"`
	Severity   Severity    `json:"severity"`
	RuleIndex  int         `json:"rule_index"`
	RuleID     string      `json:"rule_id"`
	OtherIndex int         `json:"other_index,omitempty"`
	OtherID    string      `json:"other_id,omitempty"`
	Relation   SetRelation `json:"relation,omitempty"`
	Message    string      `json:"message"`
}

// RedirectPrefix maps an owned redirect next-hop range to a tenant.
type RedirectPrefix struct {
	CIDR   net.IPNet
	Tenant string
}

// Auditor bundles ROA policy, time and redirect ownership for deterministic
// audits. The zero value is usable; set ROAs before Audit.
type Auditor struct {
	ROAs           []ROA
	Now            time.Time
	RedirectOwners []RedirectPrefix
}

// NewAuditor returns an Auditor backed by roas.
func NewAuditor(roas []ROA) *Auditor { return &Auditor{ROAs: roas} }

// Report is the result of Audit.
type Report struct {
	Decisions []AdmissionDecision `json:"decisions"`
	// Order lists rule indices from highest to lowest RFC precedence.
	Order    []int     `json:"priority_order"`
	Findings []Finding `json:"findings"`
}

// HasCode reports whether any finding has code.
func (r *Report) HasCode(code FindingCode) bool {
	for i := range r.Findings {
		if r.Findings[i].Code == code {
			return true
		}
	}
	return false
}

// FindingsFor returns findings anchored at rule index i.
func (r *Report) FindingsFor(i int) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.RuleIndex == i {
			out = append(out, f)
		}
	}
	return out
}

// Analyze runs a default audit (no clock, no redirect ownership).
func Analyze(rules []*Rule, roas []ROA) *Report {
	return NewAuditor(roas).Audit(rules)
}

// Audit performs ROA admission, RFC priority ordering and pairwise conflict
// analysis. It never mutates rules.
func (a *Auditor) Audit(rules []*Rule) *Report {
	rep := &Report{
		Decisions: AdmitAll(rules, a.ROAs, a.Now),
		Order:     PriorityOrder(rules),
	}

	a.admissionFindings(rules, rep)
	a.redirectOwnershipFindings(rules, rep)
	a.pairConflicts(rules, rep)
	return rep
}

func (a *Auditor) admissionFindings(rules []*Rule, rep *Report) {
	for i := range rules {
		d := rep.Decisions[i]
		if d.Allowed {
			continue
		}
		verdict := rules[i].Actions.Verdict()
		f := Finding{RuleIndex: i, RuleID: rules[i].label(i), Message: d.Detail}

		switch d.Code {
		case AdmitOriginMismatch:
			if verdict == VerdictDrop {
				f.Code = CodeCrossTenantBlackhole
				f.Severity = SeverityCritical
				f.Message = fmt.Sprintf("rule %s is a traffic-rate=0 blackhole but %s", f.RuleID, d.Detail)
			} else {
				f.Code = CodeROAOriginMismatch
				f.Severity = SeverityHigh
			}
		case AdmitPrefixTooWide:
			f.Code = CodePrefixTooWide
			f.Severity = SeverityHigh
		case AdmitPrefixTooLong:
			f.Code = CodePrefixTooLong
			f.Severity = SeverityMedium
		case AdmitROAExpired:
			f.Code = CodeROAExpired
			f.Severity = SeverityHigh
		case AdmitNoDestPrefix:
			f.Code = CodeMissingDestPrefix
			f.Severity = SeverityMedium
		default:
			if verdict == VerdictDrop {
				f.Code = CodeUnauthorizedBlackhole
				f.Severity = SeverityCritical
			} else {
				f.Code = CodeROANotFound
				f.Severity = SeverityHigh
			}
		}
		rep.Findings = append(rep.Findings, f)
	}
}

func (a *Auditor) redirectOwnershipFindings(rules []*Rule, rep *Report) {
	for i, r := range rules {
		if r.Actions.RedirectToIP == nil {
			continue
		}
		ip := r.Actions.RedirectToIP.IP
		owner := a.ownerOf(ip)
		if owner == "" || r.Tenant == "" || owner == r.Tenant {
			continue
		}
		rep.Findings = append(rep.Findings, Finding{
			Code:      CodeCrossTenantRedirect,
			Severity:  SeverityCritical,
			RuleIndex: i,
			RuleID:    r.label(i),
			Message: fmt.Sprintf("tenant %s redirect-to-IP %s lands in range owned by tenant %s",
				r.Tenant, ip, owner),
		})
	}
}

func (a *Auditor) ownerOf(ip net.IP) string {
	for _, rp := range a.RedirectOwners {
		if rp.CIDR.Contains(ip) {
			return rp.Tenant
		}
	}
	return ""
}

func (a *Auditor) pairConflicts(rules []*Rule, rep *Report) {
	order := rep.Order
	for pi := 0; pi < len(order); pi++ {
		for pj := pi + 1; pj < len(order); pj++ {
			hi := order[pi]
			li := order[pj]
			h := rules[hi]
			l := rules[li]
			rel := MatchRelation(h, l)
			if rel == RelDisjoint {
				continue
			}
			strictHigher := ComparePriority(h, l) < 0
			a.verdictConflict(rep, h, l, hi, li, rel, strictHigher)
			a.redirectConflict(rep, h, l, hi, li, rel, strictHigher)
		}
	}
}

func (a *Auditor) verdictConflict(rep *Report, h, l *Rule, hi, li int, rel SetRelation, strictHigher bool) {
	hv := h.Actions.Verdict()
	lv := l.Actions.Verdict()
	if hv != VerdictDrop || lv != VerdictPermit {
		return
	}
	base := Finding{
		RuleIndex:  hi,
		RuleID:     h.label(hi),
		OtherIndex: li,
		OtherID:    l.label(li),
		Relation:   rel,
	}

	switch rel {
	case RelEqual:
		if !strictHigher {
			base.Code = CodePolicyConflict
			base.Severity = SeverityHigh
			base.Message = fmt.Sprintf("equal-priority rules %s (drop) and %s (permit) cover the same traffic",
				base.RuleID, base.OtherID)
		} else {
			base.Code = CodeShadowedPermit
			base.Severity = SeverityCritical
			base.Message = fmt.Sprintf("higher-priority drop %s fully shadows permit %s",
				base.RuleID, base.OtherID)
		}
		rep.Findings = append(rep.Findings, base)
	case RelACoversB:
		base.Code = CodeShadowedPermit
		base.Severity = SeverityCritical
		base.Message = fmt.Sprintf("higher-priority drop %s is broader than and fully shadows permit %s",
			base.RuleID, base.OtherID)
		rep.Findings = append(rep.Findings, base)
	case RelBCoversA:
		base.Code = CodeShadowedPermit
		base.Severity = SeverityCritical
		base.Message = fmt.Sprintf("more-specific higher-priority drop %s carves out and shadows part of permit %s",
			base.RuleID, base.OtherID)
		rep.Findings = append(rep.Findings, base)
	case RelOverlap:
		base.Code = CodePartiallyShadowedPermit
		base.Severity = SeverityHigh
		base.Message = fmt.Sprintf("drop %s partially overlaps permit %s; some permitted traffic is blackholed",
			base.RuleID, base.OtherID)
		rep.Findings = append(rep.Findings, base)
	case RelUnknown:
		base.Code = CodePartiallyShadowedPermit
		base.Severity = SeverityMedium
		base.Message = fmt.Sprintf("drop %s may shadow permit %s (coverage not statically decidable)",
			base.RuleID, base.OtherID)
		rep.Findings = append(rep.Findings, base)
	}
}

func (a *Auditor) redirectConflict(rep *Report, h, l *Rule, hi, li int, rel SetRelation, strictHigher bool) {
	kh := redirectTargetKey(&h.Actions)
	kl := redirectTargetKey(&l.Actions)
	if kh == "" || kl == "" || kh == kl {
		return
	}
	if rel == RelDisjoint {
		return
	}
	severity := SeverityHigh
	verb := "redirects to a different target than"
	if strictHigher {
		verb = "takes precedence over and hijacks traffic redirected by"
		severity = SeverityCritical
	}
	rep.Findings = append(rep.Findings, Finding{
		Code:       CodeRedirectConflict,
		Severity:   severity,
		RuleIndex:  hi,
		RuleID:     h.label(hi),
		OtherIndex: li,
		OtherID:    l.label(li),
		Relation:   rel,
		Message: fmt.Sprintf("rule %s %s %s (%s vs %s)",
			h.label(hi), verb, l.label(li), kh, kl),
	})
}

func redirectTargetKey(a *ActionSet) string {
	if a == nil {
		return ""
	}
	if a.RedirectToIP != nil {
		return "redirect-to-ip:" + a.RedirectToIP.IP.String()
	}
	if a.Redirect != nil {
		return fmt.Sprintf("redirect-vrf:%x", a.Redirect.Target[:])
	}
	return ""
}

// MatchingRules returns indices of rules matching pkt in priority order.
func MatchingRules(pkt *Packet, rules []*Rule) []int {
	var out []int
	for _, i := range PriorityOrder(rules) {
		if RuleMatchesPacket(rules[i], pkt) {
			out = append(out, i)
		}
	}
	return out
}

// Resolve applies RFC priority and returns the highest-precedence matching
// rule, its index and effective verdict. With no matching rule it returns a
// nil rule, index -1 and VerdictPermit (normal forwarding).
func Resolve(pkt *Packet, rules []*Rule) (*Rule, int, Verdict) {
	for _, i := range MatchingRules(pkt, rules) {
		return rules[i], i, rules[i].Actions.Verdict()
	}
	return nil, -1, VerdictPermit
}
