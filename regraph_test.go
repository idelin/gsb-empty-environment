package regraph

import (
	"errors"
	"testing"
)

// counter wraps a compute function and records how many times it ran.
func counter(fn func(*Context) (any, error)) (func(*Context) (any, error), *int) {
	calls := new(int)
	return func(c *Context) (any, error) {
		*calls++
		return fn(c)
	}, calls
}

// Diamond: b and c both read a, d reads b and c. Each batch must compute
// every node exactly once.
func TestDiamondComputesOncePerBatch(t *testing.T) {
	g := New()
	a := g.Source("a", 1)
	var b, cN, d *Node

	bFn, bCalls := counter(func(c *Context) (any, error) { return c.Get(a).(int) + 1, nil })
	cFn, cCalls := counter(func(c *Context) (any, error) { return c.Get(a).(int) * 2, nil })
	dFn, dCalls := counter(func(c *Context) (any, error) {
		return c.Get(b).(int) + c.Get(cN).(int), nil
	})

	b = g.Node("b", bFn)
	cN = g.Node("c", cFn)
	d = g.Node("d", dFn)
	mustDep(t, b, a)
	mustDep(t, cN, a)
	mustDep(t, d, b)
	mustDep(t, d, cN)

	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if got := d.Value().(int); got != 4 { // (1+1) + (1*2)
		t.Fatalf("d = %d, want 4", got)
	}
	if *bCalls != 1 || *cCalls != 1 || *dCalls != 1 {
		t.Fatalf("after full eval: b=%d c=%d d=%d calls, want 1 each", *bCalls, *cCalls, *dCalls)
	}

	a.Set(5)
	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if got := d.Value().(int); got != 16 { // (5+1) + (5*2)
		t.Fatalf("d = %d, want 16", got)
	}
	if *bCalls != 2 || *cCalls != 2 || *dCalls != 2 {
		t.Fatalf("after propagation: b=%d c=%d d=%d calls, want 2 each (diamond deduped)", *bCalls, *cCalls, *dCalls)
	}
	if d.State() != Clean {
		t.Fatalf("d state = %v, want Clean", d.State())
	}
}

// A failing node must not run its downstream; downstream is marked
// Skipped, and recovers once the failure is fixed and re-propagated.
func TestFailureMarksDownstreamSkipped(t *testing.T) {
	g := New()
	src := g.Source("src", 0)
	var a, b, cN *Node

	fail := true
	aFn, aCalls := counter(func(c *Context) (any, error) {
		if fail {
			return nil, errors.New("boom")
		}
		return c.Get(src).(int) + 1, nil
	})
	bFn, bCalls := counter(func(c *Context) (any, error) { return c.Get(a).(int) * 10, nil })
	cFn, cCalls := counter(func(c *Context) (any, error) { return c.Get(b).(int) + 1, nil })
	eFn, eCalls := counter(func(c *Context) (any, error) { return 99, nil })

	a = g.Node("a", aFn)
	b = g.Node("b", bFn)
	cN = g.Node("c", cFn)
	e := g.Node("e", eFn)
	mustDep(t, a, src)
	mustDep(t, b, a)
	mustDep(t, cN, b)

	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if a.Err() == nil {
		t.Fatal("a should have failed")
	}
	if *aCalls != 1 {
		t.Fatalf("a ran %d times, want 1", *aCalls)
	}
	if *bCalls != 0 || *cCalls != 0 {
		t.Fatalf("downstream of failure must not run: b=%d c=%d", *bCalls, *cCalls)
	}
	if b.State() != Skipped || cN.State() != Skipped {
		t.Fatalf("downstream states: b=%v c=%v, want Skipped", b.State(), cN.State())
	}
	if *eCalls != 1 || e.State() != Clean {
		t.Fatalf("independent node should compute: calls=%d state=%v", *eCalls, e.State())
	}

	// Fix the failure and re-propagate through the whole graph.
	fail = false
	a.Invalidate()
	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if *aCalls != 2 || *bCalls != 1 || *cCalls != 1 {
		t.Fatalf("after recovery: a=%d b=%d c=%d, want 2/1/1", *aCalls, *bCalls, *cCalls)
	}
	if got := cN.Value().(int); got != 11 { // (0+1)*10 + 1
		t.Fatalf("c = %d, want 11", got)
	}
	if b.State() != Clean || cN.State() != Clean {
		t.Fatalf("recovered states: b=%v c=%v, want Clean", b.State(), cN.State())
	}
}

// Dependencies discovered while computing are staged: they cannot mutate
// the graph mid-traversal and only drive scheduling from the next batch.
func TestDynamicDependencyTakesEffectNextRound(t *testing.T) {
	g := New()
	x := g.Source("x", 1)
	y := g.Source("y", 10)

	mFn, mCalls := counter(func(c *Context) (any, error) {
		return c.Get(x).(int) + c.Get(y).(int), nil // y is a dynamic dep
	})
	m := g.Node("m", mFn)
	mustDep(t, m, x) // only x is declared up front

	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if *mCalls != 1 {
		t.Fatalf("m ran %d times, want 1", *mCalls)
	}
	if got := m.Value().(int); got != 11 {
		t.Fatalf("m = %d, want 11", got)
	}

	// The y edge now exists, so changing y must schedule m.
	y.Set(20)
	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if *mCalls != 2 {
		t.Fatalf("m ran %d times, want 2 (dynamic dep active)", *mCalls)
	}
	if got := m.Value().(int); got != 21 {
		t.Fatalf("m = %d, want 21", got)
	}

	// No input changed: full evaluation is a no-op.
	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if *mCalls != 2 {
		t.Fatalf("m ran %d times, want 2 (clean graph)", *mCalls)
	}
}

// Nodes created mid-batch join the graph afterwards and compute in the
// following batch.
func TestNodeCreatedDuringEvaluationDeferred(t *testing.T) {
	g := New()
	src := g.Source("src", 2)

	var inner *Node
	maker := g.Node("maker", func(c *Context) (any, error) {
		inner = g.Node("inner", func(c *Context) (any, error) {
			return c.Get(src).(int) * 3, nil
		})
		_ = inner.DependsOn(src) // staged: evaluating
		return "made", nil
	})
	mustDep(t, maker, src)

	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if inner == nil {
		t.Fatal("maker should have created inner")
	}
	if inner.State() != Dirty {
		t.Fatalf("inner state = %v, want Dirty until next batch", inner.State())
	}
	if err := g.Evaluate(); err != nil {
		t.Fatal(err)
	}
	if got := inner.Value().(int); got != 6 {
		t.Fatalf("inner = %d, want 6", got)
	}
}

func TestCycleRejection(t *testing.T) {
	g := New(WithCycleRejection())
	a := g.Node("a", nil)
	b := g.Node("b", nil)
	cN := g.Node("c", nil)

	mustDep(t, a, b)
	mustDep(t, b, cN)
	if err := cN.DependsOn(a); !errors.Is(err, ErrCycle) {
		t.Fatalf("c->a err = %v, want ErrCycle", err)
	}
	if err := a.DependsOn(a); !errors.Is(err, ErrCycle) {
		t.Fatalf("self dep err = %v, want ErrCycle", err)
	}

	// Without rejection the edge is accepted.
	g2 := New()
	x := g2.Node("x", nil)
	y := g2.Node("y", nil)
	mustDep(t, x, y)
	if err := y.DependsOn(x); err != nil {
		t.Fatalf("cycle without rejection should be allowed, got %v", err)
	}
}

// A cycle closed by a dynamically staged dependency surfaces as an error
// from Evaluate when staged changes are applied.
func TestStagedCycleReportedAfterBatch(t *testing.T) {
	g := New(WithCycleRejection())
	src := g.Source("src", 1)
	var a *Node
	b := g.Node("b", func(c *Context) (any, error) {
		v, _ := c.Get(a).(int) // stages b -> a mid-batch
		return v + 1, nil
	})
	a = g.Node("a", func(c *Context) (any, error) {
		return c.Get(src).(int), nil
	})
	mustDep(t, a, src)
	mustDep(t, a, b) // a -> b exists; b -> a would close a cycle

	if err := g.Evaluate(); !errors.Is(err, ErrCycle) {
		t.Fatalf("Evaluate err = %v, want ErrCycle", err)
	}
	if _, ok := b.deps[a]; ok {
		t.Fatal("rejected edge must not be applied")
	}
}

func mustDep(t *testing.T, n, up *Node) {
	t.Helper()
	if err := n.DependsOn(up); err != nil {
		t.Fatalf("%s -> %s: %v", n.Name(), up.Name(), err)
	}
}
