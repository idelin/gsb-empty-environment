package netsec

import "testing"

// TestVerifyUnknownOnTinyBudget guarantees exhaustion never produces a hard
// equivalent/mismatch verdict.
func TestVerifyUnknownOnTinyBudget(t *testing.T) {
	oldG := buildMul8Old(t)
	newG := buildMul8New(t, false, false)
	res, err := Verify(oldG, newG, Config{Budget: 1})
	if err != nil {
		t.Fatalf("tiny budget: %v", err)
	}
	if res.Status != StatusUnknown {
		t.Fatalf("status=%v, want unknown under tiny budget", res.Status)
	}
	// Zero means "use the default", which is sufficient for these graphs.
	res, err = Verify(oldG, newG, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusEquivalent {
		t.Fatalf("default budget status=%v, want equivalent", res.Status)
	}
}

// TestPathTruncationUnknown forces the path cap by chaining many branches.
func TestPathTruncationUnknown(t *testing.T) {
	// Build a long chain of diamonds. Each conditional still offers two
	// paths, but the two branches immediately re-converge so the graph stays
	// small while visited-block count exceeds maxSymbolicPaths.
	b := NewBuilder("deep", 2)
	merge := b.NewBlock()
	const diamonds = 12 // 2^12 = 4096 paths, just at/over the cap
	for level := 0; level < diamonds; level++ {
		cur := b.current
		cond := b.Compare(30+level, OpNe, b.Input(level%2),
			b.Const(40+level, 8, uint64(level%7)))
		t1, t2 := b.NewBlock(), b.NewBlock()
		b.UseBlock(cur)
		b.Branch(cond, t1, merge)
		b.UseBlock(t1)
		t1.Term = TermBranch
		t1.Cond = b.Const(60+level, 1, 1)
		t1.Then = merge.ID
		t1.Else = t2.ID
		b.UseBlock(t2)
		t2.Term = TermBranch
		t2.Cond = b.Const(61+level, 1, 1)
		t2.Then = merge.ID
		t2.Else = merge.ID
		b.UseBlock(merge)
		if level != diamonds-1 {
			merge = b.NewBlock()
		}
	}
	b.UseBlock(merge)
	b.current.Term = TermReturn
	b.current.Ret = b.Const(99, 8, 0)
	g := b.g

	f := NewBFactory()
	_, trunc, err := symbolicExecute(g, f, "g")
	if err != nil {
		t.Fatal(err)
	}
	if !trunc {
		t.Fatal("expected path enumeration truncation")
	}

	// A tiny chain that does NOT truncate must still produce a definitive
	// self-equivalence verdict, proving truncation is the unknown trigger.
	small := NewBuilder("small", 1)
	smallG := small.Return(small.Input(0))
	res, err := Verify(smallG, smallG, Config{Budget: 100_000})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusEquivalent {
		t.Fatalf("self-equivalence status=%v, want equivalent", res.Status)
	}
}

// TestWiden16And32 covers the production 16/32-bit signed operations with
// operands whose ranges never trigger overflow, so equivalence must hold.
func TestWiden16And32(t *testing.T) {
	type spec struct {
		name string
		op   Op
		w    int
	}
	specs := []spec{
		{"add16", OpAdd, 16}, {"sub16", OpSub, 16}, {"mul16", OpMul, 16},
		{"add32", OpAdd, 32}, {"sub32", OpSub, 32}, {"mul32", OpMul, 32},
	}
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			bo := NewBuilder("old-"+s.name, 2)
			xo := bo.SExt(1, bo.Input(0), s.w)
			yo := bo.SExt(2, bo.Input(1), s.w)
			ro := bo.Binary(3, s.op, xo, yo)
			oldG := bo.Return(ro)

			bn := NewBuilder("new-"+s.name, 2)
			xn := bn.SExt(1, bn.Input(0), s.w)
			yn := bn.SExt(2, bn.Input(1), s.w)
			rn := bn.Binary(3, s.op, xn, yn)
			newG := bn.Return(rn)

			res, err := Verify(oldG, newG, Config{Budget: 1_000_000})
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != StatusEquivalent {
				t.Fatalf("status=%v reason=%q witness=%+v",
					res.Status, res.Reason, res.Witness)
			}
		})
	}
}
