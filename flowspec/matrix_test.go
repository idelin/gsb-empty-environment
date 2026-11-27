package flowspec

import (
	"net"
	"testing"
)

func dropActions() ActionSet {
	return ActionSet{TrafficRate: &TrafficRate{Rate: 0, RateBits: 0}}
}

func redirectActions(ip string) ActionSet {
	return ActionSet{RedirectToIP: &RedirectToIP{IP: net.ParseIP(ip)}}
}

// TestDeterministicSecurityMatrix is the fixed threat/conflict matrix. Every
// row uses hard-coded fixtures and asserts stable finding codes; there is no
// randomness, clock or network dependence. Illegal operator encodings are
// covered by TestIllegalOperatorMatrix.
func TestDeterministicSecurityMatrix(t *testing.T) {
	acmeROA := ROA{Prefix: MustParsePrefix("198.51.100.0/24"), OriginAS: 65010, MinLength: 24, MaxLength: 25}

	cases := []struct {
		name      string
		roas      []ROA
		owners    []RedirectPrefix
		rules     []*Rule
		wantCodes []FindingCode
		forbid    []FindingCode
		check     func(t *testing.T, rep *Report)
	}{
		{
			name: "cross-tenant blackhole hijack shadows victim permit",
			roas: []ROA{acmeROA},
			rules: []*Rule{
				{ID: "acme-permit", Tenant: "acme", OriginAS: 65010, Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
				}},
				{ID: "evil-blackhole", Tenant: "evil", OriginAS: 65066, Actions: dropActions(), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.128/25")),
				}},
			},
			wantCodes: []FindingCode{CodeCrossTenantBlackhole, CodeShadowedPermit},
			check: func(t *testing.T, rep *Report) {
				if rep.Decisions[1].Code != AdmitOriginMismatch || rep.Decisions[0].Code != AdmitOK {
					t.Fatalf("admission codes: %s / %s", rep.Decisions[0].Code, rep.Decisions[1].Code)
				}
				if len(rep.Decisions[1].AuthorizedAS) == 0 || rep.Decisions[1].AuthorizedAS[0] != 65010 {
					t.Fatalf("expected victim AS65010 attribution, got %v", rep.Decisions[1].AuthorizedAS)
				}
				var shadow *Finding
				for i := range rep.Findings {
					if rep.Findings[i].Code == CodeShadowedPermit {
						shadow = &rep.Findings[i]
					}
				}
				if shadow == nil || shadow.RuleID != "evil-blackhole" || shadow.OtherID != "acme-permit" {
					t.Fatalf("shadow linkage wrong: %+v", shadow)
				}
				if shadow.Relation != RelBCoversA {
					t.Fatalf("expected more-specific subset relation BCoversA, got %v", shadow.Relation)
				}
			},
		},
		{
			name: "over-wide same-AS blackhole rejected",
			roas: []ROA{{Prefix: MustParsePrefix("203.0.113.0/24"), OriginAS: 65020, MaxLength: 24}},
			rules: []*Rule{
				{ID: "wide", OriginAS: 65020, Actions: dropActions(), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("203.0.112.0/23")),
				}},
			},
			wantCodes: []FindingCode{CodePrefixTooWide},
			forbid:    []FindingCode{CodeCrossTenantBlackhole},
		},
		{
			name: "equal-specificity drop vs permit is policy conflict",
			roas: []ROA{acmeROA},
			rules: []*Rule{
				{ID: "drop", OriginAS: 65010, Actions: dropActions(), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
				}},
				{ID: "permit", OriginAS: 65010, Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
				}},
			},
			wantCodes: []FindingCode{CodePolicyConflict},
			forbid:    []FindingCode{CodeShadowedPermit},
		},
		{
			name: "tcp-flag bitmask priority produces full shadow",
			roas: []ROA{acmeROA},
			rules: []*Rule{
				{ID: "syn-drop", OriginAS: 65010, Actions: dropActions(), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
					TermsComponent(TypeTCPFlags, Term{Match: true, Value: 0x02, Width: 1}),
				}},
				{ID: "synack-permit", OriginAS: 65010, Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
					TermsComponent(TypeTCPFlags, Term{Match: true, Value: 0x12, Width: 1}),
				}},
			},
			wantCodes: []FindingCode{CodeShadowedPermit},
			check: func(t *testing.T, rep *Report) {
				f := firstCode(rep, CodeShadowedPermit)
				if f == nil || f.Relation != RelACoversB {
					t.Fatalf("broad SYN must cover SYN+ACK (ACoversB), got %+v", f)
				}
			},
		},
		{
			name: "overlapping redirect-to-ip targets conflict",
			rules: []*Rule{
				{ID: "r-hi", Actions: redirectActions("198.18.0.10"), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.128/25")),
				}},
				{ID: "r-lo", Actions: redirectActions("198.18.0.20"), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
				}},
			},
			wantCodes: []FindingCode{CodeRedirectConflict},
		},
		{
			name:   "cross-tenant redirect-to-ip hijack",
			owners: []RedirectPrefix{{CIDR: MustParsePrefix("198.18.66.0/24"), Tenant: "acme"}},
			rules: []*Rule{
				{ID: "evil-redir", Tenant: "evil", Actions: redirectActions("198.18.66.66"), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
				}},
			},
			wantCodes: []FindingCode{CodeCrossTenantRedirect},
		},
		{
			name: "disjoint protocols avoid false shadow",
			roas: []ROA{acmeROA},
			rules: []*Rule{
				{ID: "tcp-drop", OriginAS: 65010, Actions: dropActions(), Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
					TermsComponent(TypeProtocol, EqualTerm(6)),
				}},
				{ID: "udp-permit", OriginAS: 65010, Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
					TermsComponent(TypeProtocol, EqualTerm(17)),
				}},
			},
			forbid: []FindingCode{CodeShadowedPermit, CodePartiallyShadowedPermit, CodePolicyConflict},
			check: func(t *testing.T, rep *Report) {
				if len(rep.Findings) != 0 {
					t.Fatalf("expected no findings for disjoint admitted rules, got %+v", rep.Findings)
				}
			},
		},
		{
			name: "clean admitted permit has no findings",
			roas: []ROA{acmeROA},
			rules: []*Rule{
				{ID: "ok", Tenant: "acme", OriginAS: 65010, Components: []Component{
					PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
				}},
			},
			check: func(t *testing.T, rep *Report) {
				if len(rep.Findings) != 0 {
					t.Fatalf("expected clean report, got %+v", rep.Findings)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Auditor{ROAs: tc.roas, RedirectOwners: tc.owners}
			rep := a.Audit(tc.rules)
			for _, code := range tc.wantCodes {
				if !rep.HasCode(code) {
					t.Fatalf("missing finding %s; all findings: %s", code, summarize(rep))
				}
			}
			for _, code := range tc.forbid {
				if rep.HasCode(code) {
					t.Fatalf("forbidden finding %s present: %s", code, summarize(rep))
				}
			}
			if tc.check != nil {
				tc.check(t, rep)
			}
		})
	}
}

func firstCode(rep *Report, code FindingCode) *Finding {
	for i := range rep.Findings {
		if rep.Findings[i].Code == code {
			return &rep.Findings[i]
		}
	}
	return nil
}

func summarize(rep *Report) string {
	out := ""
	for _, f := range rep.Findings {
		out += string(f.Code) + " "
	}
	if out == "" {
		return "<none>"
	}
	return out
}

// TestAuditOrderIndependent proves priority sorting does not depend on input
// slice order.
func TestAuditOrderIndependent(t *testing.T) {
	roas := []ROA{{Prefix: MustParsePrefix("198.51.100.0/24"), OriginAS: 65010, MaxLength: 25}}
	build := func() []*Rule {
		return []*Rule{
			{ID: "permit", OriginAS: 65010, Components: []Component{
				PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
			}},
			{ID: "drop", OriginAS: 65010, Actions: dropActions(), Components: []Component{
				PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.128/25")),
			}},
		}
	}
	r1 := NewAuditor(roas).Audit(build())
	rev := build()
	rev[0], rev[1] = rev[1], rev[0]
	r2 := NewAuditor(roas).Audit(rev)

	if len(r1.Order) != len(r2.Order) {
		t.Fatal("order length mismatch")
	}
	// Both runs must put the /25 drop first in priority order by identity.
	if r1.Order[0] != 1 || r2.Order[0] != 0 {
		t.Fatalf("priority order not stable: %v / %v", r1.Order, r2.Order)
	}
	if !r1.HasCode(CodeShadowedPermit) || !r2.HasCode(CodeShadowedPermit) {
		t.Fatal("shadow finding must occur regardless of input order")
	}
}
