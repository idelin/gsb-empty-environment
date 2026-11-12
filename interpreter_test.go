package netsec

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat: " + s)
	}
	return r
}

func TestSingleFlipDistribution(t *testing.T) {
	p := &Program{Body: []Stmt{
		Flip{NodeID: "n", Branches: []Branch{
			{Weight: rat("1/3"), Body: []Stmt{Observe{Label: "o", Expr: Lit(true)}}},
			{Weight: rat("2/3"), Body: []Stmt{Observe{Label: "o", Expr: Lit(false)}}},
		}},
	}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Paths != 2 {
		t.Fatalf("paths = %d, want 2", rep.Paths)
	}
	d := rep.Distribution
	if len(d.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(d.Points))
	}
	wantTrue := Outcome{Observations: []Observation{{Label: "o", Value: true}}}
	if got := d.ProbOf(wantTrue); got.Cmp(rat("1/3")) != 0 {
		t.Fatalf("true mass = %s, want 1/3", got)
	}
}

func TestStateMergeKeepsProvenance(t *testing.T) {
	// Two flips converge on the same state and observation; the merged node
	// must carry mass 1 while retaining two sources of 1/2 each.
	p := &Program{Body: []Stmt{
		Flip{NodeID: "coin", Branches: []Branch{
			{Weight: rat("1/2"), Body: []Stmt{Assign{Name: "x", Expr: Lit(true)}}},
			{Weight: rat("1/2"), Body: []Stmt{Assign{Name: "x", Expr: Lit(false)}}},
		}},
		Assign{Name: "x", Expr: Lit(true)},
		Observe{Label: "x", Expr: Var("x")},
	}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Paths != 2 {
		t.Fatalf("paths = %d, want 2", rep.Paths)
	}
	only := rep.Distribution.Points
	if len(only) != 1 || only[0].Prob.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("merged distribution = %+v", only)
	}

	compiled, err := p.ensureCompiled()
	if err != nil {
		t.Fatal(err)
	}
	e := &engine{prog: compiled}
	terminals, err := e.run()
	if err != nil {
		t.Fatal(err)
	}
	var sources int
	for _, tn := range terminals {
		sources += len(tn.sources)
	}
	if sources != 2 {
		t.Fatalf("retained sources = %d, want 2", sources)
	}
}

func TestNestedLoopsAndEarlyFail(t *testing.T) {
	// Outer Repeat 2 x inner Repeat 3; Fail fires on the 4th executed body
	// step, exercising nested loop unwinding and early termination.
	body := []Stmt{
		Observe{Label: "step", Expr: Lit(true)},
	}
	inner := Repeat{Count: 3, Body: body}
	p := &Program{Body: []Stmt{
		Repeat{Count: 2, Body: []Stmt{
			inner,
			Fail{Name: "boom"},
		}},
	}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Paths != 1 {
		t.Fatalf("paths = %d, want 1", rep.Paths)
	}
	pt := rep.Distribution.Points[0]
	if pt.Outcome.Exception != "boom" {
		t.Fatalf("exception = %q, want boom", pt.Outcome.Exception)
	}
	if len(pt.Outcome.Observations) != 3 {
		t.Fatalf("observations before fail = %d, want 3", len(pt.Outcome.Observations))
	}
}

func TestFailInsideProbabilisticNestedLoop(t *testing.T) {
	// Each of two rounds flips a coin; a failure branch aborts immediately.
	round := []Stmt{
		Flip{NodeID: "r", Branches: []Branch{
			{Weight: rat("1/2"), Body: []Stmt{Fail{Name: "denied"}}},
			{Weight: rat("1/2"), Body: []Stmt{Observe{Label: "ok", Expr: Lit(true)}}},
		}},
	}
	p := &Program{Body: []Stmt{Repeat{Count: 2, Body: round}}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Paths: fail round 1, fail round 2, full success.
	if rep.Paths != 3 {
		t.Fatalf("paths = %d, want 3", rep.Paths)
	}
	failure := rep.Distribution.FailureProb()
	if failure.Cmp(rat("3/4")) != 0 {
		t.Fatalf("failure = %s, want 3/4", failure)
	}
}

func TestRejectInvalidNodes(t *testing.T) {
	cases := []Flip{
		{NodeID: "bad", Branches: []Branch{
			{Weight: rat("1/2")}, {Weight: rat("1/3")},
		}},
		{NodeID: "neg", Branches: []Branch{
			{Weight: rat("-1/2")}, {Weight: rat("3/2")},
		}},
		{NodeID: "empty"},
	}
	for _, flip := range cases {
		p := &Program{Body: []Stmt{flip}}
		if _, err := p.Distribution(); err == nil {
			t.Fatalf("expected rejection for %v", flip.NodeID)
		}
	}
	p := &Program{Body: []Stmt{Flip{Branches: []Branch{{Weight: rat("1")}}}}}
	if _, err := p.Distribution(); err == nil {
		t.Fatal("expected rejection for missing node id")
	}
}

func TestWhileLimitAndSemantics(t *testing.T) {
	// A deterministic self-terminating loop runs exactly twice, exercising the
	// condition check, frame budget and clean exit.
	p := &Program{Body: []Stmt{
		Assign{Name: "done", Expr: Lit(false)},
		Assign{Name: "counted", Expr: Lit(false)},
		While{Cond: Not{X: Var("done")}, Limit: 4, Body: []Stmt{
			Observe{Label: "iter", Expr: Var("counted")},
			Assign{Name: "done", Expr: Var("counted")},
			Assign{Name: "counted", Expr: Lit(true)},
		}},
	}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Paths != 1 {
		t.Fatalf("paths = %d, want 1", rep.Paths)
	}
	if n := len(rep.Distribution.Points[0].Outcome.Observations); n != 2 {
		t.Fatalf("iterations = %d, want 2", n)
	}

	// An always-true condition exhausts the budget at the back-edge.
	over := &Program{Body: []Stmt{
		Assign{Name: "x", Expr: Lit(true)},
		While{Cond: Var("x"), Limit: 2, Body: []Stmt{Observe{Label: "i", Expr: Lit(true)}}},
	}}
	if _, err := over.Distribution(); err == nil {
		t.Fatal("expected LimitError")
	}

	// A limit of zero with a true condition is rejected on entry.
	zero := &Program{Body: []Stmt{
		Assign{Name: "x", Expr: Lit(true)},
		While{Cond: Var("x"), Limit: 0, Body: nil},
	}}
	if _, err := zero.Distribution(); err == nil {
		t.Fatal("expected LimitError for zero limit")
	}
}

func TestTwoAuthModelsFailureComparison(t *testing.T) {
	// Model A: single attempt, failure 1/100.
	modelA := &Program{Name: "A", Body: []Stmt{
		Flip{NodeID: "auth", Branches: []Branch{
			{Weight: rat("99/100"), Body: []Stmt{Observe{Label: "result", Expr: Lit(true)}}},
			{Weight: rat("1/100"), Body: []Stmt{Fail{Name: "auth_fail"}}},
		}},
	}}
	// Model B: two independent attempts; either failing aborts the run.
	attempt := []Stmt{Flip{NodeID: "auth", Branches: []Branch{
		{Weight: rat("99/100"), Body: []Stmt{Observe{Label: "result", Expr: Lit(true)}}},
		{Weight: rat("1/100"), Body: []Stmt{Fail{Name: "auth_fail"}}},
	}}}
	modelB := &Program{Name: "B", Body: []Stmt{Repeat{Count: 2, Body: attempt}}}

	ra, err := modelA.Verify()
	if err != nil {
		t.Fatal(err)
	}
	rb, err := modelB.Verify()
	if err != nil {
		t.Fatal(err)
	}
	if ra.Distribution.FailureProb().Cmp(rat("1/100")) != 0 {
		t.Fatalf("model A failure = %s", ra.Distribution.FailureProb())
	}
	wantB := rat("199/10000")
	if got := rb.Distribution.FailureProb(); got.Cmp(wantB) != 0 {
		t.Fatalf("model B failure = %s, want %s", got, wantB)
	}
}

func TestUndefinedVariable(t *testing.T) {
	p := &Program{Body: []Stmt{Observe{Label: "o", Expr: Var("missing")}}}
	if _, err := p.Distribution(); err == nil {
		t.Fatal("expected undefined variable error")
	}
	if _, err := p.Enumerate(); err == nil {
		t.Fatal("enumerator expected undefined variable error")
	}
}

func TestProbabilitiesSumToOne(t *testing.T) {
	p := &Program{Body: []Stmt{
		Flip{NodeID: "a", Branches: []Branch{
			{Weight: rat("1/7"), Body: nil},
			{Weight: rat("2/7"), Body: []Stmt{Fail{Name: "x"}}},
			{Weight: rat("4/7"), Body: nil},
		}},
	}}
	rep, err := p.Verify()
	if err != nil {
		t.Fatal(err)
	}
	sum := new(big.Rat)
	for _, pt := range rep.Distribution.Points {
		sum.Add(sum, pt.Prob)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("sum = %s", sum)
	}
}
