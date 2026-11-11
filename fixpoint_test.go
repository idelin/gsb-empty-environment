package netsec

import "testing"

// TestNarrowingRecoversBound builds i = 0; while i <= 99 { i += 1 } and
// accesses buf[i] (len 101) after the loop. Widening alone loses the
// upper bound; the descending pass must recover i == 100 at the exit.
func TestNarrowingRecoversBound(t *testing.T) {
	g := NewGraph()
	g.Array("buf", 101)
	entry := g.Entry()
	head := g.Node()
	g.Assume(entry, head, "i", 0, 0)
	body := g.Node()
	g.Assume(head, body, "i", -Inf, 99)
	g.Assign(body, head, "i", 1)
	exit := g.Node()
	g.Assume(head, exit, "i", 100, Inf)
	g.AccessAt(exit, "buf", "i", 0)

	res := Analyze(g, DefaultOptions())
	ar := res.Accesses[0]
	if !ar.Proven {
		t.Fatalf("exit access not proven: have %s / %s", ar.HaveLower, ar.HaveUpper)
	}
	if b, ok := res.Bound(exit, "i", "0"); !ok || b != 100 {
		t.Errorf("i - 0 bound at exit = %d (ok=%v), want 100", b, ok)
	}
	if ar.Concrete.Violations != 0 || !ar.Concrete.Reached {
		t.Errorf("concrete check failed: %+v", ar.Concrete)
	}
}

// TestInfeasiblePathPruned: guard x - y in [5, 3] is unsatisfiable, so
// the access behind it is vacuously proven and never concretely reached.
func TestInfeasiblePathPruned(t *testing.T) {
	g := NewGraph()
	g.Array("buf", 4)
	entry := g.Entry()
	a := g.Node()
	g.Assume(entry, a, "x", 0, 0)
	b := g.Node()
	g.Assume(a, b, "y", 0, 0)
	c := g.Node()
	g.Guard(b, c, "x", "y", 5, 3)
	g.AccessAt(c, "buf", "x", 0)

	res := Analyze(g, DefaultOptions())
	ar := res.Accesses[0]
	if !ar.Proven || !ar.Unreachable {
		t.Errorf("infeasible access: proven=%v unreachable=%v", ar.Proven, ar.Unreachable)
	}
	if ar.Concrete.Reached {
		t.Errorf("concrete interpreter reached an infeasible access")
	}
}

func TestDBMTransfer(t *testing.T) {
	m := newDBM(3) // 0, x=1, y=2
	m.guard(1, 0, 0, 0)
	m.guard(2, 0, 0, 0)
	if !m.close() {
		t.Fatal("feasible constraints reported infeasible")
	}
	m.assignAdd(1, 5)
	m.assignAdd(2, 2)
	if !m.close() {
		t.Fatal("infeasible after assigns")
	}
	if got := m.at(1, 0); got != 5 {
		t.Errorf("x - 0 = %d, want 5", got)
	}
	if got := m.at(1, 2); got != 3 {
		t.Errorf("x - y = %d, want 3", got)
	}
	m.guard(1, 2, 10, 20) // contradicts x - y = 3
	if m.close() {
		t.Error("contradictory guard should be infeasible")
	}
}

func TestBasisConstraintsBounded(t *testing.T) {
	g, _ := buildPairLoop(15, 16)
	res := Analyze(g, DefaultOptions())
	for _, ar := range res.Accesses {
		if !ar.Proven {
			t.Fatalf("access %d not proven", ar.Access.Index)
		}
		for _, c := range ar.Basis {
			if c.Bound == Inf {
				t.Errorf("basis contains unbounded constraint %s", c)
			}
		}
	}
}
