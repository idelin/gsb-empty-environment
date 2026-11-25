package certcheck

import (
	"testing"

	"prescreen.local/ps-2237843e01/capsum"
	"prescreen.local/ps-2237843e01/pathlang"
)

func asyncProgram() *capsum.Program {
	return &capsum.Program{
		Name:       "async",
		MaxHandles: 4,
		Roots: []capsum.Root{
			{Name: "ROOT", Version: "v1"},
			{Name: "TENANT", AliasOf: "ROOT", Version: "tenant-v1"},
		},
		Domains: []capsum.Domain{
			{Name: "D", Grants: []capsum.Grant{
				{Root: "ROOT", Components: []string{"box"}, Kind: capsum.GrantDesc},
			}},
		},
		EntryNode:    "main",
		EntryDomains: []string{"D"},
		Nodes:        []string{"main", "posted", "consumer", "task", "back"},
		Edges: []capsum.Edge{
			{ID: "post", From: "main", To: "posted", Kind: capsum.EdgePost, Handle: "H"},
			{ID: "lc", From: "posted", To: "consumer", Kind: capsum.EdgeLocal},
			{ID: "cons", From: "consumer", To: "task", Kind: capsum.EdgeConsume, Handle: "H", ConsumePairID: "cp"},
			{ID: "tr", From: "task", To: "back", Kind: capsum.EdgeTaskReturn, ConsumePairID: "cp"},
		},
	}
}

func buildCert(t *testing.T, p *capsum.Program) *capsum.Certificate {
	t.Helper()
	res, a, err := capsum.AnalyzeWithEvidence(p, 0)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	return res.Certificate(p, a)
}

func TestCheckAcceptsValidCertificate(t *testing.T) {
	p := asyncProgram()
	cert := buildCert(t, p)
	report := Check(p, cert)
	if !report.OK {
		t.Fatalf("valid certificate rejected: %+v", report.Failures)
	}
	if report.Checks == 0 {
		t.Fatal("no checks recorded")
	}
}

func TestCheckRejectsTamperedPointLanguage(t *testing.T) {
	p := asyncProgram()
	cert := buildCert(t, p)

	// Widen the may language at the task point to the universe; the
	// independent checker must detect the DFA mismatch.
	for i := range cert.Points {
		if cert.Points[i].Node == "task" {
			cert.Points[i].May = pathlang.Universe(cert.Alphabet)
		}
	}
	report := Check(p, cert)
	if report.OK || !hasCode(report, "points") {
		t.Fatalf("tampered point language not detected: %+v", report.Failures)
	}
}

func TestCheckRejectsBrokenPairing(t *testing.T) {
	p := asyncProgram()
	cert := buildCert(t, p)

	// Detach the task return from its consume; the structure check must fail.
	for i := range p.Edges {
		if p.Edges[i].ID == "tr" {
			p.Edges[i].ConsumePairID = "ghost"
		}
	}
	report := Check(p, cert)
	if report.OK || !hasCode(report, "structure") {
		t.Fatalf("broken pairing not detected: %+v", report.Failures)
	}
}

func TestCheckRejectsHandleOverrun(t *testing.T) {
	p := asyncProgram()
	p.MaxHandles = 0
	_, err := capsum.Analyze(p, 0)
	if err == nil {
		t.Fatal("expected handle-budget validation error")
	}
}

func TestBudgetExhaustionLeavesUnknown(t *testing.T) {
	p := asyncProgram()
	// A deliberately tiny budget must mark points unknown rather than settled.
	res, a, err := capsum.AnalyzeWithEvidence(p, 1)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !res.BudgetExhausted {
		t.Fatal("expected budget exhaustion flag")
	}
	cert := res.Certificate(p, a)
	report := Check(p, cert)
	if !report.OK {
		t.Fatalf("exhausted-budget certificate rejected: %+v", report.Failures)
	}
	// The checker accepts unknown verdicts under exhaustion but must reject any
	// settled reachable point claimed in that condition.
	for _, cp := range cert.Points {
		if cp.Status == capsum.StatusReachable {
			t.Fatalf("point %q settled under budget exhaustion", cp.Node)
		}
	}
}

func TestUnreachablePointReportedSeparately(t *testing.T) {
	p := asyncProgram()
	p.Nodes = append(p.Nodes, "isolated")
	res, a, err := capsum.AnalyzeWithEvidence(p, 0)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if res.Points["isolated"].Status != capsum.StatusUnreachable {
		t.Fatal("isolated node not reported unreachable")
	}
	cert := res.Certificate(p, a)
	report := Check(p, cert)
	if !report.OK {
		t.Fatalf("unreachable-point certificate rejected: %+v", report.Failures)
	}
}

func hasCode(r *Report, code string) bool {
	for _, f := range r.Failures {
		if f.Code == code {
			return true
		}
	}
	return false
}
