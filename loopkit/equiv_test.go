package loopkit

import "testing"

// buildEquivLoop builds a loop with a parameter-driven bound, an
// invariant pure expression, an invariant alloca load, and an
// induction update, suitable for randomized equivalence checking.
func buildEquivLoop() *Func {
	f := NewFunc("equiv")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	bound := f.AddParam("bound", I32)
	base := entry.EmitConst("base", I32, 11)
	k := entry.EmitConst("k", I32, 3)
	one := entry.EmitConst("one", I32, 1)
	zero := entry.EmitConst("zero", I32, 0)
	slot := entry.EmitAlloca("slot", I32, 1)
	addr := entry.EmitGEP(slot, 0)
	seed := entry.EmitConst("seed", I32, 41)
	entry.EmitStore(addr, seed)
	entry.SetBr(header)

	iv := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {body, nil}})
	sum := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {body, nil}})
	load := header.EmitLoad(I32, addr)
	inv := header.EmitAdd(I32, base, k) // invariant pure add
	cmp := header.EmitICmp(PredULT, iv, bound)
	header.SetCondBr(cmp, body, exit)

	acc := body.EmitAdd(I32, load, inv)
	nextIV := body.EmitAdd(I32, iv, one)
	nextSum := body.EmitAdd(I32, sum, acc)
	body.SetBr(header)
	exit.SetRet(sum)

	iv.Operands[1] = nextIV
	iv.PredBlocks[1] = body
	sum.Operands[1] = nextSum
	sum.PredBlocks[1] = body
	return f
}

func TestRandomizedEquivalence(t *testing.T) {
	orig := buildEquivLoop()
	opt := orig.Clone()
	rep := RunLICM(opt)
	if len(rep.Hoisted) == 0 {
		t.Fatal("expected hoisting in the equivalence fixture")
	}
	if err := opt.Verify(); err != nil {
		t.Fatalf("optimized IR invalid: %v", err)
	}
	mach := NewMachine()
	// Bounds span zero-iteration, small loops, and a larger loop.
	for _, b := range []int64{0, 1, 2, 3, 5, 8, 16, 31, 64} {
		if err := AssertEquivalent(mach, orig, opt, b); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDistinctAllocasNoAlias confirms loads/stores through different
// alloca bases are treated as non-aliasing, so the load still hoists.
func TestDistinctAllocasNoAlias(t *testing.T) {
	f := NewFunc("twoalloca")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	a := entry.EmitAlloca("a", I32, 1)
	b := entry.EmitAlloca("b", I32, 1)
	aAddr := entry.EmitGEP(a, 0)
	bAddr := entry.EmitGEP(b, 0)
	one := entry.EmitConst("one", I32, 1)
	two := entry.EmitConst("two", I32, 2)
	zero := entry.EmitConst("zero", I32, 0)
	limit := entry.EmitConst("limit", I32, 2)
	entry.EmitStore(aAddr, one)
	entry.EmitStore(bAddr, two)
	entry.SetBr(header)

	iv := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {body, nil}})
	loadA := header.EmitLoad(I32, aAddr) // invariant, no alias with b
	header.EmitStore(bAddr, loadA)       // stores only into distinct base b
	cmp := header.EmitICmp(PredULT, iv, limit)
	header.SetCondBr(cmp, body, exit)
	next := body.EmitAdd(I32, iv, one)
	body.SetBr(header)
	exit.SetRet(iv)
	iv.Operands[1] = next
	iv.PredBlocks[1] = body

	opt := f.Clone()
	RunLICM(opt)
	loadOpt := findInstr(opt, OpLoad, 0)
	forest := AnalyzeLoops(opt)
	if !isPreheaderOf(forest, loadOpt.Block) {
		t.Fatalf("load through distinct alloca must hoist, got %s",
			loadOpt.Block.Name)
	}
	mach := NewMachine()
	if err := AssertEquivalent(mach, f, opt); err != nil {
		t.Fatal(err)
	}
}
