package netsec

import "testing"

func TestBooleanOpsAndIte(t *testing.T) {
	f := NewBFactory()
	x, y := f.Var("x"), f.Var("y")
	ite := f.Ite(x, y, f.Not(y))
	// Ite with x=true should reduce to y; x=false to not-y. Check semantics
	// over all four assignments via the DAG evaluator.
	queries := []cnfQuery{{factory: f, roots: []*BNode{ite}}}
	budget := int64(1 << 16)
	res, model := satCheck(queries, &budget)
	if res != SatSat {
		t.Fatalf("ite sat: %v", res)
	}
	ev := newDAGEvaluator(f)
	ev.assignModel(model)
	if !ev.bool(ite) {
		t.Fatal("model must satisfy ite")
	}

	// Exhaustive truth-table check of Ite.
	for xv := 0; xv < 2; xv++ {
		for yv := 0; yv < 2; yv++ {
			ev.assignModel(map[string]bool{"x": xv == 1, "y": yv == 1})
			want := (xv == 1 && yv == 1) || (xv == 0 && yv == 0)
			if ev.bool(ite) != want {
				t.Fatalf("ite x=%d y=%d got=%v want=%v", xv, yv, ev.bool(ite), want)
			}
		}
	}
}

func TestBuilderBooleanBranches(t *testing.T) {
	b := NewBuilder("boolops", 2)
	a := b.Compare(1, OpSlt, b.Input(0), b.Const(2, 8, 10))
	c := b.Compare(3, OpSle, b.Input(1), b.Const(4, 8, 20))
	both := b.And(5, a, c)
	either := b.Or(6, a, b.Not(7, c))
	_ = either
	errBlk, okBlk := b.NewBlock(), b.NewBlock()
	b.Branch(both, errBlk, okBlk)
	b.UseBlock(errBlk)
	b.Error(9)
	b.UseBlock(okBlk)
	g := b.Return(b.Const(8, 8, 1))

	if b.Entry().ID != g.Entry {
		t.Fatal("builder entry mismatch")
	}
	// A concrete input satisfying both conditions must reach the error.
	out, err := Interpret(g, []byte{1, 5})
	if err != nil {
		t.Fatal(err)
	}
	// 1 < 10 true, but 5 <= 20 true => both => error.
	if out.Kind != OutcomeError || out.ErrNode != 9 {
		t.Fatalf("got %+v, want error node 9", out)
	}
	// Failing the first condition avoids the error.
	out, _ = Interpret(g, []byte{50, 5})
	if out.Kind != OutcomeReturn {
		t.Fatalf("got %+v, want normal return", out)
	}
}
