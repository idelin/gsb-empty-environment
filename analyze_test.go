package netsec

import (
	"fmt"
	"strings"
	"testing"
)

func mustAnalyze(t *testing.T, p *Program) *Report {
	t.Helper()
	rep, err := Analyze(p)
	if err != nil {
		t.Fatalf("Analyze: %v\nprogram:\n%s", err, ProgramString(p))
	}
	return rep
}

func TestIndependentObjectWritesAreNotReported(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		SetField(Var("o"), Lit("a"), Lit("a")),
		SetField(Var("o"), Lit("b"), NewObject()),
		Let("q", NewObject()),
		SetField(Var("q"), Lit("a"), Lit("b")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", rep.Findings)
	}
}

func TestDirectPrototypeAliasWrite(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		Let("p", Get(Var("o"), Lit("__proto__"))),
		SetField(Var("p"), Lit("a"), Lit("b")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 {
		t.Fatalf("expected one finding, got %d: %#v", len(rep.Findings), rep.Findings)
	}
	f := rep.Findings[0]
	if f.Certainty != MustWrite {
		t.Fatalf("expected must, got %s", f.Certainty)
	}
	if f.Node != p.Stmts[2] {
		t.Fatalf("finding is on the wrong node")
	}
	text := witnessText(f.Witnesses[0])
	for _, want := range []string{"allocate", "__proto__", "receiver is shared prototype"} {
		if !strings.Contains(text, want) {
			t.Fatalf("witness %q does not contain %q", text, want)
		}
	}
}

func TestConstructorPrototypeAliasWrite(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		Let("c", Get(Var("o"), Lit("constructor"))),
		Let("p", Get(Var("c"), Lit("prototype"))),
		SetField(Var("p"), Lit("b"), Lit("a")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 || rep.Findings[0].Certainty != MustWrite {
		t.Fatalf("expected one must finding, got %#v", rep.Findings)
	}
}

func TestBranchJoinMarksMay(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		If(
			&EqExpr{Left: External(), Right: Lit("a")},
			[]Stmt{Let("p", Get(Var("o"), Lit("__proto__")))},
			[]Stmt{Let("p", NewObject())},
		),
		SetField(Var("p"), Lit("a"), Lit("b")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 {
		t.Fatalf("expected one finding, got %#v", rep.Findings)
	}
	if rep.Findings[0].Certainty != MayWrite {
		t.Fatalf("expected branch-merged finding to be may, got %s", rep.Findings[0].Certainty)
	}
}

func TestProtoAssignmentToFreshObjectIsSafe(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		Let("q", NewObject()),
		SetField(Var("o"), Lit("__proto__"), Var("q")),
		SetField(Var("o"), Lit("a"), Lit("b")),
		Let("p", Get(Var("o"), Lit("__proto__"))),
		SetField(Var("p"), Lit("b"), Lit("a")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", rep.Findings)
	}
}

func TestOwnConstructorShadowsDefault(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		SetField(Var("o"), Lit("constructor"), NewObject()),
		Let("p", Get(Get(Var("o"), Lit("constructor")), Lit("prototype"))),
		SetField(Var("p"), Lit("a"), Lit("b")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", rep.Findings)
	}
}

func TestFieldAliasesPropagateToSharedPrototype(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		SetField(Var("o"), Lit("a"), Get(Var("o"), Lit("__proto__"))),
		SetField(Get(Var("o"), Lit("a")), Lit("b"), Lit("a")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 || rep.Findings[0].Certainty != MustWrite {
		t.Fatalf("expected field alias to be reported, got %#v", rep.Findings)
	}
}

func TestFieldAliasToIndependentObjectIsSafe(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		Let("q", NewObject()),
		SetField(Var("o"), Lit("a"), Var("q")),
		SetField(Get(Var("o"), Lit("a")), Lit("b"), Lit("a")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", rep.Findings)
	}
}

func TestExternalKeyWritesSharedPrototype(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		Let("p", Get(Var("o"), Lit("__proto__"))),
		Let("k", External()),
		SetField(Var("p"), Var("k"), Lit("a")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 || rep.Findings[0].Certainty != MustWrite {
		t.Fatalf("expected one must finding, got %#v", rep.Findings)
	}
	if len(rep.Findings[0].Keys) != len(stringDomain) {
		t.Fatalf("expected all external keys, got %v", rep.Findings[0].Keys)
	}
}

func TestImpossibleBranchIsPruned(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		If(&EqExpr{Left: Lit("a"), Right: Lit("b")},
			[]Stmt{Let("p", Get(Var("o"), Lit("__proto__")))},
			[]Stmt{Let("p", NewObject())},
		),
		SetField(Var("p"), Lit("a"), Lit("b")),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 0 {
		t.Fatalf("impossible branch produced a finding: %#v", rep.Findings)
	}
}

func TestImpossibleLoopIsPruned(t *testing.T) {
	p := Seq(
		Let("o", NewObject()),
		While(
			&EqExpr{Left: Lit("a"), Right: Lit("b")},
			16,
			SetField(Get(Var("o"), Lit("__proto__")), Lit("a"), Lit("b")),
		),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 0 {
		t.Fatalf("impossible loop produced a finding: %#v", rep.Findings)
	}
}

func TestLoopAllocationEqualityIsNotAssumed(t *testing.T) {
	alloc := NewObject()
	danger := SetField(
		Get(Var("o"), Lit("__proto__")),
		Lit("a"),
		Lit("b"),
	)
	p := Seq(
		Let("o", alloc),
		Repeat(2,
			Let("p", Var("o")),
			Let("o", alloc),
			If(
				&EqExpr{Left: Var("o"), Right: Var("p")},
				[]Stmt{Let("safe", Lit("a"))},
				[]Stmt{danger},
			),
		),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 || rep.Findings[0].Node != danger {
		t.Fatalf("expected loop instance inequality to keep the else branch, got %#v", rep.Findings)
	}
	res, err := CheckSoundness(p, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.MissedNodes) != 0 {
		t.Fatalf("missed concrete dangerous nodes: %v", res.MissedNodes)
	}
}

func TestLoopWriteToSharedPrototype(t *testing.T) {
	body := SetField(
		Get(Var("o"), Lit("__proto__")),
		Lit("a"),
		Lit("b"),
	)
	p := Seq(
		Let("o", NewObject()),
		Repeat(16, body),
	)
	rep := mustAnalyze(t, p)
	if len(rep.Findings) != 1 {
		t.Fatalf("expected the loop body write, got %#v", rep.Findings)
	}
	if rep.Findings[0].Node != body {
		t.Fatalf("finding is on the wrong node")
	}
}

func TestConcreteAndStaticAgreeOnSmallPrograms(t *testing.T) {
	programs := []*Program{
		Seq(Let("o", NewObject()), Let("p", Get(Var("o"), Lit("__proto__"))), SetField(Var("p"), Lit("a"), Lit("b"))),
		Seq(Let("o", NewObject()), SetField(Var("o"), Lit("constructor"), NewObject()), SetField(Get(Get(Var("o"), Lit("constructor")), Lit("prototype")), Lit("a"), Lit("b"))),
		Seq(Let("o", NewObject()), SetField(Var("o"), Lit("__proto__"), NewObject()), SetField(Var("o"), Lit("a"), Lit("b"))),
		Seq(Let("o", NewObject()), Let("p", Get(Var("o"), Lit("__proto__"))), Let("k", External()), SetField(Var("p"), Var("k"), Lit("a"))),
		Seq(
			Let("o", NewObject()),
			If(&EqExpr{Left: External(), Right: Lit("__proto__")},
				[]Stmt{Let("p", Get(Var("o"), Lit("__proto__")))},
				[]Stmt{Let("p", NewObject())},
			),
			SetField(Var("p"), Lit("a"), Lit("a")),
		),
	}
	for i, p := range programs {
		res, err := CheckSoundness(p, 10000)
		if err != nil {
			t.Fatalf("program %d: %v", i, err)
		}
		if len(res.MissedNodes) != 0 {
			t.Fatalf("program %d has missed nodes %v\n%s", i, res.MissedNodes, ProgramString(p))
		}
	}
}

func TestExhaustiveSmallProgramFamily(t *testing.T) {
	// A finite, exhaustive family of straight-line programs over the
	// interesting operations, plus one branch and one loop template. Every
	// external-string assignment is enumerated by CheckSoundness.
	statements := []Stmt{
		Let("o", NewObject()),
		Let("q", NewObject()),
		Let("p", Get(Var("o"), Lit("__proto__"))),
		Let("p", Get(Get(Var("o"), Lit("constructor")), Lit("prototype"))),
		Let("p", NewObject()),
		SetField(Var("o"), Lit("__proto__"), Var("q")),
		SetField(Var("o"), External(), Lit("a")),
		SetField(Var("p"), Lit("a"), Lit("b")),
		SetField(Var("p"), External(), Lit("a")),
		SetField(Var("o"), Lit("a"), Var("q")),
	}

	check := func(p *Program) {
		t.Helper()
		res, err := CheckSoundness(p, 20000)
		if err != nil {
			t.Fatalf("CheckSoundness: %v\n%s", err, ProgramString(p))
		}
		if len(res.MissedNodes) != 0 {
			t.Fatalf("missed concrete dangerous nodes %v\n%s", res.MissedNodes, ProgramString(p))
		}
	}

	for _, a := range statements {
		check(Seq(a))
		for _, b := range statements {
			check(Seq(a, b))
			for _, c := range statements {
				check(Seq(a, b, c))
			}
		}
	}

	branch := If(&EqExpr{Left: External(), Right: Lit("__proto__")},
		[]Stmt{Let("p", Get(Var("o"), Lit("__proto__")))},
		[]Stmt{Let("p", Var("q"))},
	)
	check(Seq(Let("o", NewObject()), Let("q", NewObject()), branch, SetField(Var("p"), Lit("b"), Lit("a"))))
	check(Seq(Let("o", NewObject()), Repeat(4, SetField(Get(Var("o"), Lit("__proto__")), Lit("a"), Lit("b")))))
}

func TestValidationRejectsUnsupportedPrograms(t *testing.T) {
	cases := []*Program{
		Seq(Let("o", NewObject()), SetField(Var("o"), Lit("x"), Lit("a"))),
		Seq(While(&EqExpr{Left: Lit("a"), Right: Lit("a")}, 17, Let("o", NewObject()))),
		Seq(Repeat(17, Let("o", NewObject()))),
	}
	for i, p := range cases {
		if err := Validate(p); err == nil {
			t.Fatalf("case %d should have failed validation", i)
		}
	}

	var stmts []Stmt
	for i := 0; i < MaxObjects+1; i++ {
		stmts = append(stmts, Let(fmt.Sprintf("o%d", i), NewObject()))
	}
	if err := Validate(Seq(stmts...)); err == nil {
		t.Fatal("object-count limit was not enforced")
	}

	stmts = stmts[:MaxObjects]
	if err := Validate(Seq(stmts...)); err != nil {
		t.Fatalf("exactly %d objects should be valid: %v", MaxObjects, err)
	}
	if err := Validate(Seq(While(&EqExpr{Left: Lit("a"), Right: Lit("a")}, MaxLoopIterations))); err != nil {
		t.Fatalf("loop bound %d should be valid: %v", MaxLoopIterations, err)
	}
}
