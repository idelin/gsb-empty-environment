package loopkit

import "testing"

// TestSideEffectCallBlocksLoad ensures an impure call in the loop
// suppresses load hoisting even with no aliasing store.
func TestSideEffectCallBlocksLoad(t *testing.T) {
	f := NewFunc("callblock")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	slot := entry.EmitAlloca("slot", I32, 1)
	addr := entry.EmitGEP(slot, 0)
	nine := entry.EmitConst("nine", I32, 9)
	entry.EmitStore(addr, nine)
	zero := entry.EmitConst("zero", I32, 0)
	one := entry.EmitConst("one", I32, 1)
	limit := entry.EmitConst("limit", I32, 2)
	entry.SetBr(header)

	iv := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {body, nil}})
	load := header.EmitLoad(I32, addr)
	header.EmitCall("write", Void, load) // unregistered => side effect
	cmp := header.EmitICmp(PredULT, iv, limit)
	header.SetCondBr(cmp, body, exit)
	next := body.EmitAdd(I32, iv, one)
	body.SetBr(header)
	exit.SetRet(iv)
	iv.Operands[1] = next
	iv.PredBlocks[1] = body

	opt := f.Clone()
	rep := RunLICM(opt)
	loadOpt := findInstr(opt, OpLoad, 0)
	if loadOpt.Block.Name != "header" {
		t.Fatalf("load must stay in header due to side-effecting call, got %s",
			loadOpt.Block.Name)
	}
	found := false
	for _, b := range rep.BlockedAlias {
		if b == loadOpt {
			found = true
		}
	}
	if !found {
		t.Fatal("expected load reported as alias/call blocked")
	}
}

// TestPureCallHoisting registers a pure call and requires it to hoist
// only when its block dominates all loop exits.
func TestPureCallHoisting(t *testing.T) {
	MarkPureCall("abs")
	f := NewFunc("purecall")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	base := entry.EmitConst("base", I32, -5)
	zero := entry.EmitConst("zero", I32, 0)
	limit := entry.EmitConst("limit", I32, 2)
	entry.SetBr(header)

	iv := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {body, nil}})
	call := header.EmitCall("abs", I32, base)
	cmp := header.EmitICmp(PredULT, iv, limit)
	header.SetCondBr(cmp, body, exit)
	next := body.EmitAdd(I32, iv, call)
	body.SetBr(header)
	exit.SetRet(iv)
	iv.Operands[1] = next
	iv.PredBlocks[1] = body

	opt := f.Clone()
	RunLICM(opt)
	callOpt := findInstr(opt, OpCall, 0)
	forest := AnalyzeLoops(opt)
	if !isPreheaderOf(forest, callOpt.Block) {
		t.Fatalf("pure dominating call must hoist, got block %s",
			callOpt.Block.Name)
	}
	mach := NewMachine()
	mach.Hosts["abs"] = func(a []int64) (int64, error) {
		if a[0] < 0 {
			return -a[0], nil
		}
		return a[0], nil
	}
	if err := AssertEquivalent(mach, f, opt); err != nil {
		t.Fatal(err)
	}
}

// TestDomTreeSanity checks reflexive dominance and that an entry
// dominates every reachable block.
func TestDomTreeSanity(t *testing.T) {
	f := buildNestedLoop()
	d := ComputeDomTree(f)
	entry := f.Entry()
	if !d.Dominates(entry, entry) {
		t.Fatal("dominance must be reflexive")
	}
	for _, b := range f.Blocks {
		if !d.Dominates(entry, b) {
			t.Fatalf("entry must dominate %s", b.Name)
		}
	}
	if d.Dominates(blockNamed(f, "inner.body"), blockNamed(f, "entry")) {
		t.Fatal("inner body cannot dominate entry")
	}
}

// TestNestedLoopDepth checks parent/child and depth relationships.
func TestNestedLoopDepth(t *testing.T) {
	f := buildNestedLoop()
	forest := AnalyzeLoops(f)
	depth := map[string]int{}
	var outer *Loop
	for _, l := range forest.Loops {
		depth[l.Header.Name] = l.Depth
		if l.Header.Name == "outer.header" {
			outer = l
		}
	}
	if depth["outer.header"] != 1 || depth["inner.header"] != 2 {
		t.Fatalf("unexpected depths %v", depth)
	}
	if len(outer.Children) != 1 {
		t.Fatalf("outer must have one child, got %d", len(outer.Children))
	}
	if outer.Children[0].Header.Name != "inner.header" {
		t.Fatal("outer child must be inner loop")
	}
}

// TestExistingPreheaderRecognized verifies a single-edge preheader is
// reused rather than duplicated.
func TestExistingPreheaderRecognized(t *testing.T) {
	f := buildNestedLoop()
	forest := AnalyzeLoops(f)
	made := forest.EnsurePreheaders()
	if made != 0 {
		t.Fatalf("fixture already has preheaders, made=%d", made)
	}
	blocksBefore := len(f.Blocks)
	forest2 := AnalyzeLoops(f)
	if made2 := forest2.EnsurePreheaders(); made2 != 0 {
		t.Fatalf("idempotent preheader pass made=%d", made2)
	}
	if len(f.Blocks) != blocksBefore {
		t.Fatal("preheader count changed on idempotent run")
	}
}

// TestTripCountDoWhile covers a post-tested do-while loop where the
// body always runs at least once.
func TestTripCountDoWhile(t *testing.T) {
	f := NewFunc("dowhile")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	latch := f.NewBlock("latch")
	exit := f.NewBlock("exit")

	st := entry.EmitConst("start", I32, 0)
	step := entry.EmitConst("step", I32, 2)
	bound := entry.EmitConst("bound", I32, 9)
	entry.SetBr(header)
	iv := header.EmitPhi(I32, []PhiEdge{{entry, st}, {latch, nil}})
	header.SetBr(latch)
	cmp := latch.EmitICmp(PredULT, iv, bound)
	next := latch.EmitAdd(I32, iv, step)
	latch.SetCondBr(cmp, header, exit)
	exit.SetRet(iv)
	iv.Operands[1] = next
	iv.PredBlocks[1] = latch

	forest := AnalyzeLoops(f)
	if len(forest.Loops) != 1 || len(forest.Loops[0].BackEdges) != 1 ||
		forest.Loops[0].BackEdges[0] != latch {
		t.Fatal("do-while must form one natural loop with latch backedge")
	}
	scev := NewSCEV(forest)
	res := scev.TripCount(forest.Loops[0])
	// Values 0,2,4,6,8 continue; 10 fails -> 5 trips.
	if res.Kind != TripFinite || res.Count != 5 {
		t.Fatalf("do-while trip = %+v, want finite 5", res)
	}
	mach := NewMachine()
	if r := mach.Run(f); r.Trap != nil || r.Ret != 10 {
		t.Fatalf("interpreter do-while result=%+v want ret 10", r)
	}
}

// TestSwappedOperandExit exercises swapPred when the induction
// variable appears on the right-hand side of the comparison.
func TestSwappedOperandExit(t *testing.T) {
	f := NewFunc("swap")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	st := entry.EmitConst("start", I32, 0)
	step := entry.EmitConst("step", I32, 1)
	bound := entry.EmitConst("bound", I32, 4)
	entry.SetBr(header)
	iv := header.EmitPhi(I32, []PhiEdge{{entry, st}, {body, nil}})
	// bound > iv (true while iv < bound): operands are swapped.
	cmp := header.EmitICmp(PredUGT, bound, iv)
	header.SetCondBr(cmp, body, exit)
	next := body.EmitAdd(I32, iv, step)
	body.SetBr(header)
	exit.SetRet(iv)
	iv.Operands[1] = next
	iv.PredBlocks[1] = body

	forest := AnalyzeLoops(f)
	scev := NewSCEV(forest)
	res := scev.TripCount(forest.Loops[0])
	if res.Kind != TripFinite || res.Count != 4 {
		t.Fatalf("swapped-operand trip = %+v, want finite 4", res)
	}
}

// TestUnsignedNegativeStepMirror checks the ring-mirror reduction for
// a downward-counting unsigned loop.
func TestUnsignedNegativeStepMirror(t *testing.T) {
	f := NewFunc("uneg")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	// Count down 10,8,6,4,2 then 0 fails "iv != 0" style guard; use UGT.
	st := entry.EmitConst("start", I32, 10)
	step := entry.EmitConst("step", I32, -2) // modular 0xFFFFFFFE
	zero := entry.EmitConst("zero", I32, 0)
	entry.SetBr(header)
	iv := header.EmitPhi(I32, []PhiEdge{{entry, st}, {body, nil}})
	cmp := header.EmitICmp(PredUGT, iv, zero)
	header.SetCondBr(cmp, body, exit)
	next := body.EmitAdd(I32, iv, step)
	body.SetBr(header)
	exit.SetRet(iv)
	iv.Operands[1] = next
	iv.PredBlocks[1] = body

	forest := AnalyzeLoops(f)
	scev := NewSCEV(forest)
	res := scev.TripCount(forest.Loops[0])
	// 10,8,6,4,2 continue; 0 fails -> 5 trips.
	if res.Kind != TripFinite || res.Count != 5 {
		t.Fatalf("unsigned negative-step trip = %+v, want finite 5", res)
	}
	mach := NewMachine()
	if r := mach.Run(f); r.Trap != nil {
		t.Fatalf("unexpected trap %v", r.Trap)
	}
}

// TestULEFullRingInfinite covers the ULE boundary where bound == MAX,
// so every representable value satisfies the predicate forever.
func TestULEFullRingInfinite(t *testing.T) {
	maxU32 := int64(0xFFFFFFFF)
	f := tripLoop(I32, PredULE, 0, 1, maxU32)
	forest := AnalyzeLoops(f)
	scev := NewSCEV(forest)
	res := scev.TripCount(forest.Loops[0])
	if res.Kind != TripInfinite {
		t.Fatalf("ULE to MAX must be infinite, got %+v", res)
	}
}
