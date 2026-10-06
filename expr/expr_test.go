package expr_test

import (
	"strings"
	"testing"

	"gauge/decimal"
	"gauge/expr"
)

func scalePtr(s int) *int { return &s }

func constNode(s string) expr.Node {
	return expr.Node{Op: expr.OpConst, Value: decimal.MustParse(s)}
}

func mustEval(t *testing.T, g *expr.Graph, id string) decimal.Value {
	t.Helper()
	v, _, err := g.Eval(id)
	if err != nil {
		t.Fatalf("Eval(%q): %v", id, err)
	}
	return v
}

func stepCounts(steps []expr.Step, id string) (computed, cached int) {
	for _, s := range steps {
		if s.NodeID != id {
			continue
		}
		if s.Cached {
			cached++
		} else {
			computed++
		}
	}
	return
}

func TestSharedSubexpressionEvaluatedOnce(t *testing.T) {
	g := expr.NewGraph()
	g.Set("a", constNode("1.5"))
	g.Set("b", constNode("2"))
	g.Set("s", expr.Node{Op: expr.OpMul, A: "a", B: "b"})
	g.Set("root", expr.Node{Op: expr.OpMul, A: "s", B: "s"})

	v, steps, err := g.Eval("root")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "9" {
		t.Fatalf("root = %s, want 9", v)
	}
	computed, cached := stepCounts(steps, "s")
	if computed != 1 || cached != 1 {
		t.Fatalf("shared node s: computed=%d cached=%d, want 1/1", computed, cached)
	}
	// From-scratch evaluation agrees with the cached one.
	g.ClearCache()
	fresh := mustEval(t, g, "root")
	if fresh.Cmp(v) != 0 {
		t.Fatalf("from-scratch %s != cached %s", fresh, v)
	}
}

func TestNegativeMidpointRounding(t *testing.T) {
	g := expr.NewGraph()
	g.Set("m", expr.Node{Op: expr.OpConst, Value: decimal.MustParse("-2.5"), Scale: scalePtr(0)})
	g.Set("m2", expr.Node{Op: expr.OpConst, Value: decimal.MustParse("-0.125"), Scale: scalePtr(2)})

	v, steps, err := g.Eval("m")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "-2" {
		t.Fatalf("round(-2.5, 0) = %s, want -2", v)
	}
	if len(steps) != 1 || !steps[0].Rounded || !strings.Contains(steps[0].Evidence, "tie=true") {
		t.Fatalf("missing tie evidence: %+v", steps)
	}
	if steps[0].Exact != "-2.5" || steps[0].Value != "-2" {
		t.Fatalf("step exact/value = %s/%s", steps[0].Exact, steps[0].Value)
	}

	v2, _, err := g.Eval("m2")
	if err != nil {
		t.Fatal(err)
	}
	if v2.String() != "-0.12" {
		t.Fatalf("round(-0.125, 2) = %s, want -0.12", v2)
	}
}

func TestOnlyMarkedNodesRound(t *testing.T) {
	third, err := decimal.NewRational(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	g := expr.NewGraph()
	g.Set("third", expr.Node{Op: expr.OpConst, Value: third})
	g.Set("thirdR", expr.Node{Op: expr.OpConst, Value: third, Scale: scalePtr(2)})
	g.Set("three", constNode("3"))
	g.Set("exact", expr.Node{Op: expr.OpMul, A: "third", B: "three"})
	g.Set("rounded", expr.Node{Op: expr.OpMul, A: "thirdR", B: "three"})

	if v := mustEval(t, g, "exact"); v.String() != "1" {
		t.Fatalf("unmarked node must stay exact: (1/3)*3 = %s, want 1", v)
	}
	if v := mustEval(t, g, "rounded"); v.String() != "0.99" {
		t.Fatalf("marked node rounds first: 0.33*3 = %s, want 0.99", v)
	}
}

func TestCacheInvalidationOnModify(t *testing.T) {
	g := expr.NewGraph()
	g.Set("a", constNode("1"))
	g.Set("b", constNode("2"))
	g.Set("s", expr.Node{Op: expr.OpMul, A: "a", B: "b"})
	g.Set("t", expr.Node{Op: expr.OpMul, A: "s", B: "a"})
	g.Set("u", expr.Node{Op: expr.OpMul, A: "b", B: "b"})

	if v := mustEval(t, g, "t"); v.String() != "2" {
		t.Fatalf("t = %s, want 2", v)
	}
	if v := mustEval(t, g, "u"); v.String() != "4" {
		t.Fatalf("u = %s, want 4", v)
	}

	// Modifying a invalidates a, s and t, but not the unrelated u/b.
	g.Set("a", constNode("3"))
	v, steps, err := g.Eval("t")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "18" {
		t.Fatalf("after modify, t = %s, want 18", v)
	}
	for _, id := range []string{"s", "t"} {
		if computed, cached := stepCounts(steps, id); computed != 1 || cached != 0 {
			t.Fatalf("node %s: computed=%d cached=%d, want 1/0", id, computed, cached)
		}
	}
	// a is referenced twice (by s and t): recomputed once, then cached.
	if computed, cached := stepCounts(steps, "a"); computed != 1 || cached != 1 {
		t.Fatalf("node a: computed=%d cached=%d, want 1/1", computed, cached)
	}
	_, stepsU, err := g.Eval("u")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stepsU {
		if !s.Cached {
			t.Fatalf("unrelated node %s recomputed after modifying a", s.NodeID)
		}
	}

	// From-scratch evaluation of the modified graph agrees.
	g.ClearCache()
	if fresh := mustEval(t, g, "t"); fresh.String() != "18" {
		t.Fatalf("from-scratch t = %s, want 18", fresh)
	}
}

func TestResumableEvaluation(t *testing.T) {
	build := func() *expr.Graph {
		g := expr.NewGraph()
		g.Set("c0", constNode("1.25"))
		g.Set("c1", constNode("-2.5"))
		prev := "c0"
		for i, f := range []string{"2", "0.5", "4", "0.25", "8", "1.5"} {
			fc := "f" + string(rune('0'+i))
			g.Set(fc, constNode(f))
			id := "n" + string(rune('0'+i))
			g.Set(id, expr.Node{Op: expr.OpMul, A: prev, B: fc})
			prev = id
		}
		g.Set("mix", expr.Node{Op: expr.OpMul, A: prev, B: "c1"})
		return g
	}

	g := build()
	e := expr.NewEvaluator(g, "mix")
	var v decimal.Value
	suspensions := 0
	for {
		var err error
		v, err = e.Run(2) // budget: two node completions per call
		if err == nil {
			break
		}
		if err != expr.ErrSuspended {
			t.Fatalf("Run: %v", err)
		}
		suspensions++
		if suspensions > 100 {
			t.Fatal("evaluation did not converge")
		}
	}
	if suspensions == 0 {
		t.Fatal("expected at least one suspension")
	}

	// Same value as an uninterrupted from-scratch run, same step trace.
	ref := build()
	rv, rsteps, err := ref.Eval("mix")
	if err != nil {
		t.Fatal(err)
	}
	if v.Cmp(rv) != 0 {
		t.Fatalf("resumed %s != from-scratch %s", v, rv)
	}
	steps := e.Steps()
	if len(steps) != len(rsteps) {
		t.Fatalf("resumed %d steps != from-scratch %d steps", len(steps), len(rsteps))
	}
	for i := range steps {
		if steps[i] != rsteps[i] {
			t.Fatalf("step %d differs: %+v vs %+v", i, steps[i], rsteps[i])
		}
	}
	// 1.25*2*0.5*4*0.25*8*1.5*(-2.5) = -37.5
	if v.String() != "-37.5" {
		t.Fatalf("mix = %s, want -37.5", v)
	}
}

func TestFromScratchConsistencyWithRounding(t *testing.T) {
	g := expr.NewGraph()
	g.Set("base", expr.Node{Op: expr.OpConst, Value: decimal.MustParse("-0.375"), Scale: scalePtr(2)})
	g.Set("k", constNode("7"))
	g.Set("shared", expr.Node{Op: expr.OpMul, A: "base", B: "k"})
	g.Set("root", expr.Node{Op: expr.OpMul, A: "shared", B: "shared"})

	v1 := mustEval(t, g, "root")
	// base rounds -0.375 -> -0.38 (tie, away from zero); shared = -2.66;
	// root = 7.0756 exactly.
	if v1.String() != "7.0756" {
		t.Fatalf("root = %s, want 7.0756", v1)
	}

	// Modify the shared subexpression's factor; successors recompute.
	g.Set("k", constNode("3"))
	v2 := mustEval(t, g, "root")
	if v2.String() != "1.2996" {
		t.Fatalf("after modify, root = %s, want 1.2996", v2)
	}

	// From-scratch evaluation of the final graph agrees.
	g.ClearCache()
	if fresh := mustEval(t, g, "root"); fresh.Cmp(v2) != 0 {
		t.Fatalf("from-scratch %s != incremental %s", fresh, v2)
	}
}

func TestDivisionByZero(t *testing.T) {
	g := expr.NewGraph()
	g.Set("a", constNode("1"))
	g.Set("z", constNode("0"))
	g.Set("bad", expr.Node{Op: expr.OpDiv, A: "a", B: "z"})
	if _, _, err := g.Eval("bad"); err == nil ||
		!strings.Contains(err.Error(), "division by zero") {
		t.Fatalf("expected division-by-zero error, got %v", err)
	}
}

func TestUndefinedNode(t *testing.T) {
	g := expr.NewGraph()
	g.Set("root", expr.Node{Op: expr.OpMul, A: "missing", B: "alsomissing"})
	if _, _, err := g.Eval("root"); err == nil ||
		!strings.Contains(err.Error(), "undefined node") {
		t.Fatalf("expected undefined-node error, got %v", err)
	}
}
