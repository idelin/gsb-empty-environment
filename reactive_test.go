package reactive

import (
	"errors"
	"testing"
)

func sum(inputs []interface{}) (interface{}, error) {
	total := 0
	for _, v := range inputs {
		total += v.(int)
	}
	return total, nil
}

// Diamond: s -> b, s -> c, {b,c} -> d. Each node must compute exactly
// once per batch, and d must see both branches' fresh values.
func TestDiamondComputedOnce(t *testing.T) {
	g := NewGraph()
	s := g.Node("s", nil)
	b := g.Node("b", func(in []interface{}) (interface{}, error) { return in[0].(int) + 1, nil })
	c := g.Node("c", func(in []interface{}) (interface{}, error) { return in[0].(int) * 2, nil })
	d := g.Node("d", sum)
	mustAddDep(t, g, b, s)
	mustAddDep(t, g, c, s)
	mustAddDep(t, g, d, b)
	mustAddDep(t, g, d, c)

	if err := g.Set(s, 1); err != nil {
		t.Fatal(err)
	}
	g.Evaluate()

	if got := d.Value().(int); got != 2+2 { // b=2, c=2
		t.Fatalf("d = %d, want 4", got)
	}
	for _, n := range []*Node{b, c, d} {
		if n.Calls != 1 {
			t.Fatalf("%s computed %d times, want exactly 1", n.Name(), n.Calls)
		}
	}

	// No input changed: a second batch computes nothing.
	g.Evaluate()
	for _, n := range []*Node{b, c, d} {
		if n.Calls != 1 {
			t.Fatalf("%s recomputed without changes (calls=%d)", n.Name(), n.Calls)
		}
	}

	// Full re-evaluation after a new input: still exactly once each.
	if err := g.Set(s, 10); err != nil {
		t.Fatal(err)
	}
	g.Evaluate()
	if got := d.Value().(int); got != 11+20 {
		t.Fatalf("d = %d, want 31", got)
	}
	for _, n := range []*Node{b, c, d} {
		if n.Calls != 2 {
			t.Fatalf("%s computed %d times after 2 batches, want 2", n.Name(), n.Calls)
		}
	}
}

// A failing node must not crash the batch; its downstream is marked
// skipped and never computed, while siblings still run.
func TestFailureMarksDownstream(t *testing.T) {
	g := NewGraph()
	boom := errors.New("boom")
	a := g.Node("a", func([]interface{}) (interface{}, error) { return nil, boom })
	b := g.Node("b", func(in []interface{}) (interface{}, error) { return in[0], nil })
	c := g.Node("c", sum)
	ok := g.Node("ok", func([]interface{}) (interface{}, error) { return 42, nil })
	mustAddDep(t, g, b, a)
	mustAddDep(t, g, c, b)

	g.Evaluate()

	if a.Err() != boom {
		t.Fatalf("a.Err = %v, want boom", a.Err())
	}
	for _, n := range []*Node{b, c} {
		if !n.Skipped() {
			t.Fatalf("%s should be marked skipped", n.Name())
		}
		if n.Err() != ErrUpstreamFailed {
			t.Fatalf("%s.Err = %v, want ErrUpstreamFailed", n.Name(), n.Err())
		}
		if n.Calls != 0 {
			t.Fatalf("%s compute ran %d times, want 0", n.Name(), n.Calls)
		}
	}
	if ok.Calls != 1 || ok.Value().(int) != 42 {
		t.Fatalf("unrelated node should still compute, calls=%d", ok.Calls)
	}
}

// Cycles and self-dependencies are rejected at configuration time.
func TestCycleRejected(t *testing.T) {
	g := NewGraph()
	x := g.Node("x", sum)
	y := g.Node("y", sum)
	z := g.Node("z", sum)
	mustAddDep(t, g, y, x)
	mustAddDep(t, g, z, y)

	if err := g.AddDep(x, z); err != ErrCycle {
		t.Fatalf("x<-z err = %v, want ErrCycle", err)
	}
	if err := g.AddDep(x, x); err != ErrCycle {
		t.Fatalf("self dep err = %v, want ErrCycle", err)
	}
	if err := g.AddDep(y, x); err != ErrDuplicateDep {
		t.Fatalf("dup dep err = %v, want ErrDuplicateDep", err)
	}
	// Rejected edges must not corrupt the graph.
	if err := g.Set(x, 1); err != nil {
		t.Fatal(err)
	}
	g.Evaluate()
	if z.Value().(int) != 1 {
		t.Fatalf("z = %v, want 1", z.Value())
	}
}

// Structural changes during a batch are rejected; a dependency added
// between batches only takes effect on the next Evaluate.
func TestDynamicDepNextRound(t *testing.T) {
	g := NewGraph()
	s1 := g.Node("s1", nil)
	s2 := g.Node("s2", nil)

	var addErr error
	hook := g.Node("hook", func([]interface{}) (interface{}, error) {
		// Attempting to rewire the graph mid-traversal must fail.
		addErr = g.AddDep(g.nodes[len(g.nodes)-1], s2)
		return 0, nil
	})
	total := g.Node("total", sum)
	mustAddDep(t, g, total, s1)
	mustAddDep(t, g, hook, s1)

	if err := g.Set(s1, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.Set(s2, 100); err != nil {
		t.Fatal(err)
	}
	g.Evaluate()
	if addErr != ErrGraphBusy {
		t.Fatalf("in-batch AddDep err = %v, want ErrGraphBusy", addErr)
	}
	if total.Value().(int) != 1 {
		t.Fatalf("total = %v, want 1 (s2 not wired yet)", total.Value())
	}

	// Between batches the edge is accepted and marks total dirty.
	if err := g.AddDep(total, s2); err != nil {
		t.Fatal(err)
	}
	g.Evaluate()
	if total.Value().(int) != 101 {
		t.Fatalf("total = %v, want 101 after dynamic dep", total.Value())
	}
	if total.Calls != 2 {
		t.Fatalf("total calls = %d, want 2", total.Calls)
	}
}

func mustAddDep(t *testing.T, g *Graph, n, dep *Node) {
	t.Helper()
	if err := g.AddDep(n, dep); err != nil {
		t.Fatalf("AddDep(%s, %s): %v", n.Name(), dep.Name(), err)
	}
}
