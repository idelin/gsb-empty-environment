package flowsec

import "testing"

func testLattice(t *testing.T) (*Lattice, int, int) {
	t.Helper()
	l, err := ChainLattice("Public", "Secret")
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := l.Index("Public")
	sec, _ := l.Index("Secret")
	return l, pub, sec
}

func sinkByName(r *Report, name string) SinkResult {
	for _, s := range r.Sinks {
		if s.Name == name {
			return s
		}
	}
	panic("no sink " + name)
}

func leakProgram(pub, sec int) *Program {
	return &Program{
		Receivers: []string{"alice", "bob"},
		Instrs: []Instr{
			&Input{ID: 1, Name: "secret", Level: sec, Receivers: []string{"alice"}},
			&Const{ID: 2, Name: "zero"},
			&Const{ID: 3, Name: "one"},
			&Assign{ID: 4, Dst: "leak", Srcs: []string{"secret"}},
			&Sink{ID: 5, Name: "direct", Src: "leak", Receiver: "bob", MaxLevel: pub},
			&Assign{ID: 6, Dst: "x", Srcs: []string{"zero"}},
			&If{ID: 7, Cond: "secret",
				Then: []Instr{&Assign{ID: 8, Dst: "x", Srcs: []string{"one"}}},
				Else: []Instr{&Assign{ID: 9, Dst: "x", Srcs: []string{"zero"}}},
			},
			&Sink{ID: 10, Name: "implicit", Src: "x", Receiver: "bob", MaxLevel: pub},
			&Sink{ID: 11, Name: "alice-ok", Src: "leak", Receiver: "alice", MaxLevel: sec},
		},
	}
}

func TestDirectLeakDenied(t *testing.T) {
	l, pub, sec := testLattice(t)
	rep := Analyze(l, leakProgram(pub, sec))
	s := sinkByName(rep, "direct")
	if s.Allowed {
		t.Fatal("direct leak to bob must be denied")
	}
	if len(s.Reasons) == 0 {
		t.Fatal("expected denial reasons")
	}
	// shortest chain: secret#1 --data--> leak#1 --data--> sink
	if len(s.Chain) != 3 {
		t.Fatalf("expected 3-hop chain, got %v", s.Chain)
	}
	if s.Chain[0].Kind != "input" || s.Chain[2].Kind != "sink" {
		t.Fatalf("chain endpoints wrong: %v", s.Chain)
	}
}

func TestImplicitFlowSurvivesBranchJoin(t *testing.T) {
	l, pub, sec := testLattice(t)
	rep := Analyze(l, leakProgram(pub, sec))
	s := sinkByName(rep, "implicit")
	if s.Allowed {
		t.Fatal("implicit flow via pc must be denied after branch join")
	}
	// the only path from x back to a secret root goes through a pc edge
	hasPC := false
	for _, st := range s.Chain {
		if st.ViaRole == "pc" {
			hasPC = true
		}
	}
	if !hasPC {
		t.Fatalf("expected pc edge in chain, got %v", s.Chain)
	}
}

func TestAuthorizedReceiverAllowed(t *testing.T) {
	l, pub, sec := testLattice(t)
	rep := Analyze(l, leakProgram(pub, sec))
	if !sinkByName(rep, "alice-ok").Allowed {
		t.Fatal("alice at Secret level must be allowed")
	}
}

func TestDeclassifyRequiresAuthorization(t *testing.T) {
	l, pub, sec := testLattice(t)
	prog := leakProgram(pub, sec)
	prog.Instrs = append(prog.Instrs,
		&Declassify{ID: 20, Dst: "y", Src: "leak", ToLevel: pub, AddReceivers: []string{"bob"}, Token: "ok"},
		&Sink{ID: 21, Name: "declassified", Src: "y", Receiver: "bob", MaxLevel: pub},
		&Declassify{ID: 22, Dst: "z", Src: "leak", ToLevel: pub, AddReceivers: []string{"bob"}, Token: "forged"},
		&Sink{ID: 23, Name: "forged", Src: "z", Receiver: "bob", MaxLevel: pub},
	)
	prog.Policy = Policy{Grants: map[int]Grant{
		20: {Token: "ok", ToLevel: pub, AddReceivers: []string{"bob"}},
		// no grant for 22
	}}
	rep := Analyze(l, prog)

	if !sinkByName(rep, "declassified").Allowed {
		t.Fatal("authorized declassification must be allowed")
	}
	prov := sinkByName(rep, "declassified").Label.Prov
	if len(prov) != 1 || prov[0].AtInstr != 20 || prov[0].ToLevel != pub {
		t.Fatalf("provenance not recorded: %v", prov)
	}
	if sinkByName(rep, "forged").Allowed {
		t.Fatal("unauthorized declassification must not shrink the label")
	}
	if len(rep.Violations) == 0 {
		t.Fatal("expected a recorded violation for the forged declassification")
	}
}

func TestDeclassifyCannotRaise(t *testing.T) {
	l, pub, sec := testLattice(t)
	prog := &Program{
		Receivers: []string{"bob"},
		Instrs: []Instr{
			&Const{ID: 1, Name: "c"},
			&Declassify{ID: 2, Dst: "d", Src: "c", ToLevel: sec, Token: "ok"},
			&Sink{ID: 3, Name: "s", Src: "d", Receiver: "bob", MaxLevel: pub},
		},
		Policy: Policy{Grants: map[int]Grant{2: {Token: "ok", ToLevel: sec}}},
	}
	rep := Analyze(l, prog)
	if !sinkByName(rep, "s").Allowed {
		t.Fatal("non-shrinking declassify must be rejected, label stays Public")
	}
	if len(rep.Violations) == 0 {
		t.Fatal("expected violation for non-shrinking declassify")
	}
}

func TestReceiverRestrictionWithoutLevel(t *testing.T) {
	l, pub, _ := testLattice(t)
	prog := &Program{
		Receivers: []string{"alice", "bob"},
		Instrs: []Instr{
			&Input{ID: 1, Name: "v", Level: pub, Receivers: []string{"alice"}},
			&Sink{ID: 2, Name: "to-bob", Src: "v", Receiver: "bob", MaxLevel: pub},
			&Sink{ID: 3, Name: "to-alice", Src: "v", Receiver: "alice", MaxLevel: pub},
		},
	}
	rep := Analyze(l, prog)
	if sinkByName(rep, "to-bob").Allowed {
		t.Fatal("receiver restriction must deny bob even at Public level")
	}
	if !sinkByName(rep, "to-alice").Allowed {
		t.Fatal("alice must be allowed")
	}
}

func TestCrossValidationAgreement(t *testing.T) {
	l, pub, sec := testLattice(t)
	prog := leakProgram(pub, sec)
	prog.Instrs = append(prog.Instrs,
		&Declassify{ID: 20, Dst: "y", Src: "leak", ToLevel: pub, AddReceivers: []string{"bob"}, Token: "ok"},
		&Sink{ID: 21, Name: "declassified", Src: "y", Receiver: "bob", MaxLevel: pub},
	)
	prog.Policy = Policy{Grants: map[int]Grant{20: {Token: "ok", ToLevel: pub, AddReceivers: []string{"bob"}}}}

	rep := Analyze(l, prog)
	sets, _ := AnalyzeSets(l, prog)
	if mm := CrossValidate(rep, sets); len(mm) > 0 {
		t.Fatalf("runs disagree: %v", mm)
	}
}
