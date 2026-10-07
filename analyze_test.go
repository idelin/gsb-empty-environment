package iflow

import (
	"strings"
	"testing"
)

func secretInput(lat *Lattice) map[string]Label {
	return map[string]Label{"secret": NewLabel(lat, "Secret")}
}

func chainHas(chain []string, substr string) bool {
	for _, s := range chain {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func TestExplicitLeakDenied(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			Assign("x", V("secret")),
			Sink("out", "public", V("x")),
		},
	}
	rep := Analyze(prog, lat)
	if len(rep.Sinks) != 1 {
		t.Fatalf("want 1 sink verdict, got %d", len(rep.Sinks))
	}
	v := rep.Sinks[0]
	if v.Allowed {
		t.Fatal("explicit leak to public must be denied")
	}
	if len(v.Chain) != 3 ||
		!strings.Contains(v.Chain[0], "input secret") ||
		!strings.Contains(v.Chain[1], "x := secret") ||
		!strings.Contains(v.Chain[2], "sink out") {
		t.Fatalf("unexpected chain: %v", v.Chain)
	}
}

func TestImplicitLeakViaBranchDenied(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			Assign("x", Const(0)),
			If(Bin("gt", V("secret"), Const(0)),
				[]Instr{Assign("x", Const(1))}, nil),
			Sink("out", "public", V("x")),
		},
	}
	rep := Analyze(prog, lat)
	v := rep.Sinks[0]
	if v.Allowed {
		t.Fatal("implicit leak through branch must be denied")
	}
	if !chainHas(v.Chain, "control dependency: if (secret gt 0)") {
		t.Fatalf("chain should mention control dependency: %v", v.Chain)
	}
}

func TestPcLabelSurvivesBranchEnd(t *testing.T) {
	lat := chainLattice(t)
	// result is a constant assigned AFTER the branch; because the pc label is
	// not dropped at branch end, it is still tainted. This is a deliberate
	// over-approximation, not a claim that the program leaks.
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			If(Bin("gt", V("secret"), Const(0)),
				[]Instr{Assign("tmp", Const(1))}, nil),
			Assign("result", Const(42)),
			Sink("out", "public", V("result")),
		},
	}
	rep := Analyze(prog, lat)
	if rep.Sinks[0].Allowed {
		t.Fatal("pc label must not be dropped at branch end")
	}
	if !chainHas(rep.Sinks[0].Chain, "control dependency") {
		t.Fatalf("chain should go through the branch: %v", rep.Sinks[0].Chain)
	}
}

func TestAuthorizedDeclass(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs:         secretInput(lat),
		Authorizations: map[string]bool{"ticket-1": true},
		Body: []Instr{
			Declass("y", "secret", "Internal", []string{"audit"}, "ticket-1"),
			Sink("auditLog", "audit", V("y")),
			Sink("pubOut", "public", V("y")),
		},
	}
	rep := Analyze(prog, lat)
	if len(rep.Declass) != 1 || !rep.Declass[0].OK {
		t.Fatalf("declass should be honored: %+v", rep.Declass)
	}
	ev := rep.Declass[0]
	if ev.From != "Secret[]" || ev.To != "Internal[audit]" {
		t.Fatalf("declass labels wrong: %s => %s", ev.From, ev.To)
	}
	if !rep.Sinks[0].Allowed {
		t.Fatal("audit sink must be allowed after authorized declass")
	}
	if rep.Sinks[1].Allowed {
		t.Fatal("public sink must still be denied after declass to audit only")
	}
	if !chainHas(rep.Sinks[0].Chain, "declassify secret -> y") {
		t.Fatalf("chain should record the declass node: %v", rep.Sinks[0].Chain)
	}
}

func TestUnauthorizedDeclassRejected(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs: secretInput(lat),
		Body: []Instr{
			Declass("y", "secret", "Public", []string{"public"}, "forged"),
			Sink("out", "public", V("y")),
		},
	}
	rep := Analyze(prog, lat)
	if len(rep.Declass) != 1 || rep.Declass[0].OK {
		t.Fatal("unauthorized declass must be rejected")
	}
	if len(rep.Violations) == 0 {
		t.Fatal("rejection must be recorded as a violation")
	}
	if rep.Sinks[0].Allowed {
		t.Fatal("label must stay Secret after rejected declass")
	}
}

func TestDeclassMustShrink(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs:         map[string]Label{"low": NewLabel(lat, "Internal", "audit")},
		Authorizations: map[string]bool{"ticket-1": true},
		Body: []Instr{
			Declass("y", "low", "Secret", nil, "ticket-1"),
		},
	}
	rep := Analyze(prog, lat)
	if rep.Declass[0].OK {
		t.Fatal("raising the level is not a shrink and must be rejected")
	}
	if !strings.Contains(rep.Declass[0].Reason, "not a shrink") {
		t.Fatalf("unexpected reason: %s", rep.Declass[0].Reason)
	}
}

func TestPublicFlowAllowed(t *testing.T) {
	lat := chainLattice(t)
	prog := &Program{
		Inputs: map[string]Label{"pub": BottomLabel(lat)},
		Body: []Instr{
			Assign("x", Bin("add", V("pub"), Const(1))),
			Sink("out", "public", V("x")),
		},
	}
	rep := Analyze(prog, lat)
	if !rep.Sinks[0].Allowed {
		t.Fatal("public flow to public sink must be allowed")
	}
	if len(rep.Sinks[0].Chain) != 0 {
		t.Fatalf("no taint chain expected for public data: %v", rep.Sinks[0].Chain)
	}
}
