package dag

import (
	"context"
	"math/big"
	"testing"

	"gaugeconv/exact"
)

func mustEval(t *testing.T, e *Evaluator, n *Node) *big.Rat {
	t.Helper()
	v, err := e.Eval(context.Background(), n)
	if err != nil {
		t.Fatalf("Eval(%s): %v", n.Name, err)
	}
	return v
}

func countComputed(steps []*Step, name string) (computed, cached int) {
	for _, s := range steps {
		if s.Node == name {
			if s.Cached {
				cached++
			} else {
				computed++
			}
		}
	}
	return
}

func TestSharedSubexpressionComputedOnce(t *testing.T) {
	shared := NewOp("shared", Mul,
		NewInput("a", "1.5"),
		NewConst("third", 1, 3)) // 1/3 stays exact: unmarked nodes never round
	left := NewOp("left", Add, shared, NewInput("b", "0.25"))
	right := NewOp("right", Mul, shared, NewConst("two", 2, 1))
	root := NewOp("root", Sub, left, right)

	e := NewEvaluator()
	v := mustEval(t, e, root)

	fresh, err := EvalFresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if v.Cmp(fresh) != 0 {
		t.Fatalf("cached eval %s != fresh eval %s", v.RatString(), fresh.RatString())
	}
	computed, _ := countComputed(e.Steps(), "shared")
	if computed != 1 {
		t.Fatalf("shared node computed %d times, want 1", computed)
	}
	// root = (1/2 + 1/4) - (1/2 * 2) = 3/4 - 1 = -1/4, exact
	if v.RatString() != "-1/4" {
		t.Fatalf("root = %s, want -1/4", v.RatString())
	}
}

func TestOnlyMarkedNodesRound(t *testing.T) {
	third := NewConst("third", 1, 3) // unmarked: stays exact
	sum := NewOp("sum", Add, third, third)
	rounded := NewOp("rounded", Add, sum, NewInput("x", "0.005")).WithRound(1)

	e := NewEvaluator()
	v := mustEval(t, e, rounded)

	// sum must be exactly 2/3, not a decimal approximation
	var sumStep *Step
	for _, s := range e.Steps() {
		if s.Node == "sum" {
			sumStep = s
		}
	}
	if sumStep == nil || sumStep.Value.RatString() != "2/3" {
		t.Fatalf("sum step = %v, want exact 2/3", sumStep)
	}
	if sumStep.Evidence != nil {
		t.Fatalf("unmarked node unexpectedly carries rounding evidence")
	}
	// rounded: 2/3 + 0.005 = 0.671666... -> 0.7
	if got := exact.FormatFixed(v, 1); got != "0.7" {
		t.Fatalf("rounded = %s, want 0.7", got)
	}
	// and the rounded node must carry evidence
	var rStep *Step
	for _, s := range e.Steps() {
		if s.Node == "rounded" {
			rStep = s
		}
	}
	if rStep == nil || rStep.Evidence == nil || rStep.PreRound == nil {
		t.Fatalf("rounded node missing evidence: %+v", rStep)
	}
	if rStep.PreRound.RatString() != "403/600" {
		t.Fatalf("pre-round = %s, want 403/600", rStep.PreRound.RatString())
	}
}

func TestNegativeMidpointEvidence(t *testing.T) {
	in := NewInput("gauge", "-2.5")
	r := NewOp("r", Neg, in).WithRound(0)                            // -(-2.5) = 2.5 -> tie to even -> 2
	neg := NewOp("neg", Mul, in, NewConst("one", 1, 1)).WithRound(0) // -2.5 -> -2

	e := NewEvaluator()
	v1 := mustEval(t, e, r)
	v2 := mustEval(t, e, neg)
	if v1.RatString() != "2" {
		t.Fatalf("round(2.5) = %s, want 2", v1.RatString())
	}
	if v2.RatString() != "-2" {
		t.Fatalf("round(-2.5) = %s, want -2", v2.RatString())
	}
	var negStep *Step
	for _, s := range e.Steps() {
		if s.Node == "neg" {
			negStep = s
		}
	}
	if negStep == nil || negStep.Evidence == nil {
		t.Fatal("missing evidence for neg node")
	}
	ev := negStep.Evidence
	if !ev.Tie || ev.Direction != exact.RoundTieToEven {
		t.Fatalf("evidence = %s, want tie resolved to even", ev)
	}
	if ev.Input.Sign() != -1 {
		t.Fatalf("evidence input sign = %d, want negative", ev.Input.Sign())
	}

	// -0.5 rounds to zero and must not be negative zero
	half := NewInput("h", "-0.5")
	hr := NewOp("hr", Neg, half).WithRound(0)
	mh := NewOp("mh", Mul, half, NewConst("one2", 1, 1)).WithRound(0)
	e2 := NewEvaluator()
	if v := mustEval(t, e2, mh); v.Sign() != 0 || exact.FormatFixed(v, 0) != "0" {
		t.Fatalf("round(-0.5) = %s (%s), want 0", v.RatString(), exact.FormatFixed(v, 0))
	}
	if v := mustEval(t, e2, hr); v.Sign() != 0 {
		t.Fatalf("round(0.5) = %s, want 0 (tie to even)", v.RatString())
	}
	oneHalf := NewInput("oh", "-1.5")
	ohr := NewOp("ohr", Neg, oneHalf).WithRound(0)
	if v := mustEval(t, e2, ohr); v.RatString() != "2" {
		t.Fatalf("round(1.5) = %s, want 2", v.RatString())
	}
}

func TestMutationInvalidatesTransitiveSuccessors(t *testing.T) {
	a := NewInput("a", "1.0")
	b := NewOp("b", Mul, a, NewConst("k", 3, 2))
	c := NewOp("c", Add, b, NewConst("off", 1, 4)).WithRound(1)
	d := NewOp("d", Mul, c, NewConst("two", 2, 1))
	unrelated := NewOp("unrelated", Add, NewConst("u", 1, 1), NewConst("v", 2, 1))

	e := NewEvaluator()
	v1 := mustEval(t, e, d)
	mustEval(t, e, unrelated)
	// b = 3/2; c = 7/4 = 1.75 -> round(1) half-even -> 1.8 = 9/5; d = 18/5
	if v1.RatString() != "18/5" {
		t.Fatalf("initial d = %s, want 18/5", v1.RatString())
	}

	e.ResetSteps()
	a.SetDecimal("2.5")
	v2 := mustEval(t, e, d)
	mustEval(t, e, unrelated)

	// b = 2.5*3/2 = 15/4; c = 15/4+1/4 = 4 -> round -> 4; d = 8
	if v2.RatString() != "8" {
		t.Fatalf("after mutation d = %s, want 8", v2.RatString())
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		computed, _ := countComputed(e.Steps(), name)
		if computed != 1 {
			t.Fatalf("node %s recomputed %d times after mutation, want 1", name, computed)
		}
	}
	// unrelated node must still be served from cache
	if computed, cached := countComputed(e.Steps(), "unrelated"); computed != 0 || cached != 1 {
		t.Fatalf("unrelated node recomputed (computed=%d cached=%d)", computed, cached)
	}

	// cross-check against from-scratch evaluation
	fresh, err := EvalFresh(d)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Cmp(fresh) != 0 {
		t.Fatalf("incremental %s != fresh %s", v2.RatString(), fresh.RatString())
	}
}

func TestSetRoundInvalidates(t *testing.T) {
	in := NewInput("g", "0.125")
	r := NewOp("r", Mul, in, NewConst("one", 1, 1)).WithRound(2)
	e := NewEvaluator()
	if v := mustEval(t, e, r); v.RatString() != "3/25" { // 0.12
		t.Fatalf("round2(0.125) = %s, want 3/25", v.RatString())
	}
	r.SetRound(&RoundSpec{Places: 1})
	if v := mustEval(t, e, r); v.RatString() != "1/10" { // 0.1
		t.Fatalf("round1(0.125) = %s, want 1/10", v.RatString())
	}
	r.SetRound(nil)
	if v := mustEval(t, e, r); v.RatString() != "1/8" {
		t.Fatalf("unrounded = %s, want exact 1/8", v.RatString())
	}
}

// cancelAfter cancels the context after n calls to Err.
type cancelAfter struct {
	context.Context
	n *int
}

func (c cancelAfter) Err() error {
	*c.n--
	if *c.n < 0 {
		return context.Canceled
	}
	return c.Context.Err()
}

func TestResumableEvaluation(t *testing.T) {
	// wide shared DAG
	shared := NewOp("shared", Add, NewInput("s1", "0.5"), NewInput("s2", "0.25"))
	mk := func(name string, k int64) *Node {
		return NewOp(name, Mul, shared, NewConst(name+"c", k, 7)).WithRound(3)
	}
	root := NewOp("root", Add,
		NewOp("p", Add, mk("m1", 3), mk("m2", 5)),
		NewOp("q", Add, mk("m3", 11), mk("m4", 13)))

	// first pass: cancel after 4 node evaluations
	n := 8
	e := NewEvaluator()
	if _, err := e.Eval(cancelAfter{context.Background(), &n}, root); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	partial := len(e.Steps())
	if partial == 0 {
		t.Fatal("no partial progress cached")
	}

	// resume with a fresh context: must complete and match from-scratch eval
	e.ResetSteps()
	v, err := e.Eval(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := EvalFresh(root)
	if err != nil {
		t.Fatal(err)
	}
	if v.Cmp(fresh) != 0 {
		t.Fatalf("resumed %s != fresh %s", v.RatString(), fresh.RatString())
	}
	// some steps of the resumed run must have been cache hits
	anyCached := false
	for _, s := range e.Steps() {
		if s.Cached {
			anyCached = true
		}
	}
	if !anyCached {
		t.Fatal("resumed run did not reuse any cached nodes")
	}
}

func TestDivideByZeroNode(t *testing.T) {
	d := NewOp("d", Div, NewInput("n", "1.0"), NewInput("z", "0.0"))
	e := NewEvaluator()
	_, err := e.Eval(context.Background(), d)
	if err == nil {
		t.Fatal("expected divide-by-zero error")
	}
	if _, ferr := EvalFresh(d); ferr == nil {
		t.Fatal("fresh eval expected divide-by-zero error")
	}
}

func TestFreshMatchesCachedAcrossMutations(t *testing.T) {
	a := NewInput("a", "3.14159")
	k := NewConst("k", 22, 7)
	prod := NewOp("prod", Mul, a, k).WithRound(4)
	quot := NewOp("quot", Div, prod, NewConst("d", 355, 113)).WithRound(6)

	e := NewEvaluator()
	for _, dec := range []string{"3.14159", "-2.71828", "0.00005", "99999.99999"} {
		a.SetDecimal(dec)
		v := mustEval(t, e, quot)
		fresh, err := EvalFresh(quot)
		if err != nil {
			t.Fatal(err)
		}
		if v.Cmp(fresh) != 0 {
			t.Fatalf("input %s: incremental %s != fresh %s", dec, v.RatString(), fresh.RatString())
		}
	}
}
