package netsec

import "testing"

// TestExhaustive8AddSubOverflow checks the symbolic add/sub overflow flags
// against the interpreter across the full 8-bit input space.
func TestExhaustive8AddSubOverflow(t *testing.T) {
	for _, op := range []Op{OpAdd, OpSub} {
		t.Run(op.String(), func(t *testing.T) {
			b := NewBuilder("ovf-"+op.String(), 2)
			r := b.Binary(3, op, b.Input(0), b.Input(1))
			ovf := b.Overflow(4, r)
			errBlk, okBlk := b.NewBlock(), b.NewBlock()
			b.Branch(ovf, errBlk, okBlk)
			b.UseBlock(errBlk)
			b.Error(7)
			b.UseBlock(okBlk)
			g := b.Return(r)

			f := NewBFactory()
			outs, _, err := symbolicExecute(g, f, "g")
			if err != nil {
				t.Fatal(err)
			}
			var ovfGuard *BNode
			for _, o := range outs {
				if o.kind == OutcomeError {
					ovfGuard = o.guard
				}
			}
			ev := newDAGEvaluator(f)
			for a := 0; a < 256; a++ {
				for c := 0; c < 256; c++ {
					in := []byte{byte(a), byte(c)}
					ev.assignInputs(in)
					out, err := Interpret(g, in)
					if err != nil {
						t.Fatal(err)
					}
					want := out.Kind == OutcomeError
					if ev.bool(ovfGuard) != want {
						t.Fatalf("op=%s in=%v symbolic=%v want=%v", op, in, ev.bool(ovfGuard), want)
					}
				}
			}
		})
	}
}

func TestValidateRejectsBadGraphs(t *testing.T) {
	t.Run("too many inputs", func(t *testing.T) {
		g := &Graph{Inputs: MaxInputs + 1, Entry: 0,
			Blocks: []*Block{{ID: 0, Term: TermError, ErrNode: 1}},
			Values: map[int]*Value{}}
		if g.Validate() == nil {
			t.Fatal("expected input-count error")
		}
	})

	t.Run("control-flow cycle", func(t *testing.T) {
		g := &Graph{Inputs: 0, Entry: 0, Values: map[int]*Value{
			1: {ID: 1, Op: OpConst, Width: 8, Imm: 0},
			2: {ID: 2, Op: OpConst, Width: 1, Imm: 1},
		}}
		b0 := &Block{ID: 0, Insts: []int{2}, Term: TermBranch, Cond: 2, Then: 1, Else: 1}
		b1 := &Block{ID: 1, Insts: []int{1}, Term: TermBranch, Cond: 2, Then: 0, Else: 0}
		g.Blocks = []*Block{b0, b1}
		if g.Validate() == nil {
			t.Fatal("expected cycle error")
		}
	})

	t.Run("width mismatch arithmetic", func(t *testing.T) {
		g := &Graph{Inputs: 1, Entry: 0, Values: map[int]*Value{
			1: {ID: 1, Op: OpInput, Width: 8, Input: 0},
			2: {ID: 2, Op: OpConst, Width: 16, Imm: 1},
			3: {ID: 3, Op: OpAdd, Width: 16, X: 1, Y: 2},
		}}
		g.Blocks = []*Block{{ID: 0, Insts: []int{1, 2, 3},
			Term: TermReturn, Ret: 3}}
		if g.Validate() == nil {
			t.Fatal("expected width-mismatch error")
		}
	})

	t.Run("non-dominating use", func(t *testing.T) {
		g := &Graph{Inputs: 1, Entry: 0, Values: map[int]*Value{
			1: {ID: 1, Op: OpInput, Width: 8, Input: 0},
			2: {ID: 2, Op: OpConst, Width: 8, Imm: 1},
			3: {ID: 3, Op: OpConst, Width: 1, Imm: 1},
			4: {ID: 4, Op: OpAdd, Width: 8, X: 1, Y: 2},
		}}
		b0 := &Block{ID: 0, Insts: []int{3}, Term: TermBranch, Cond: 3, Then: 1, Else: 2}
		b1 := &Block{ID: 1, Insts: []int{1, 2, 4}, Term: TermReturn, Ret: 4}
		b2 := &Block{ID: 2, Insts: []int{2}, Term: TermReturn, Ret: 2}
		g.Blocks = []*Block{b0, b1, b2}
		if err := g.Validate(); err == nil {
			t.Fatal("expected dominance error")
		}
	})
}

func TestInterpretInputCountGuard(t *testing.T) {
	b := NewBuilder("g", 1)
	g := b.Return(b.Input(0))
	if _, err := Interpret(g, []byte{1, 2}); err == nil {
		t.Fatal("expected input-count error")
	}
}

func TestZeroInputConstGraphs(t *testing.T) {
	bo := NewBuilder("const-eq-old", 0)
	oldG := bo.Return(bo.Const(1, 16, 42))
	bn := NewBuilder("const-eq-new", 0)
	newG := bn.Return(bn.Const(2, 16, 42))
	res, err := Verify(oldG, newG, Config{Budget: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusEquivalent {
		t.Fatalf("equal constants status=%v", res.Status)
	}

	bm := NewBuilder("const-mismatch", 0)
	mismatchG := bm.Return(bm.Const(3, 16, 43))
	res, err = Verify(oldG, mismatchG, Config{Budget: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusMismatch || res.Witness == nil || len(res.Witness.Inputs) != 0 {
		t.Fatalf("constant mismatch status=%v witness=%+v", res.Status, res.Witness)
	}
}
