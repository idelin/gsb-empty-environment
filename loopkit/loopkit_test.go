package loopkit

import (
	"strings"
	"testing"
)

func findInstr(f *Func, op Op, nth int) *Instr {
	count := 0
	for _, b := range f.Blocks {
		for _, i := range b.Instrs {
			if i.Op == op {
				if count == nth {
					return i
				}
				count++
			}
		}
	}
	return nil
}

func blockNamed(f *Func, name string) *Block {
	for _, b := range f.Blocks {
		if b.Name == name {
			return b
		}
	}
	return nil
}

func assertInBlock(t *testing.T, i *Instr, want *Block, label string) {
	t.Helper()
	if i.Block != want {
		t.Fatalf("%s: want block %s, got %s", label, want.Name, i.Block.Name)
	}
}

// buildNestedLoop builds:
//
//	entry -> outer.ph -> outer.header -> inner.ph -> inner.header
//	  inner.body -> inner.header / inner.exit
//	  inner.exit -> outer.latch -> outer.header / outer.exit -> ret
func buildNestedLoop() *Func {
	f := NewFunc("nested")
	p := f.Params
	_ = p
	entry := f.NewBlock("entry")
	oh := f.NewBlock("outer.header")
	iph := f.NewBlock("inner.ph")
	ih := f.NewBlock("inner.header")
	ib := f.NewBlock("inner.body")
	iexit := f.NewBlock("inner.exit")
	olatch := f.NewBlock("outer.latch")
	oexit := f.NewBlock("outer.exit")

	base := entry.EmitConst("base", I32, 100)
	k1 := entry.EmitConst("k1", I32, 7)
	limitI := entry.EmitConst("limitI", I32, 4)
	limitO := entry.EmitConst("limitO", I32, 3)
	zero := entry.EmitConst("zero", I32, 0)
	one := entry.EmitConst("one", I32, 1)
	entry.SetBr(oh)

	oi := oh.EmitPhi(I32, []PhiEdge{{entry, zero}, {olatch, nil}})
	ocmp := oh.EmitICmp(PredULT, oi, limitO)
	oh.SetCondBr(ocmp, iph, oexit)

	iph.SetBr(ih)
	ii := ih.EmitPhi(I32, []PhiEdge{{iph, zero}, {ib, nil}})
	icmp := ih.EmitICmp(PredULT, ii, limitI)
	ih.SetCondBr(icmp, ib, iexit)

	inv := ib.EmitAdd(I32, base, k1) // loop-invariant pure add
	ii2 := ib.EmitAdd(I32, ii, one)
	acc := ib.EmitAdd(I32, inv, ii2)
	_ = acc
	ib.SetBr(ih)

	iexit.SetBr(olatch)

	oi2 := olatch.EmitAdd(I32, oi, one)
	olatch.SetBr(oh)
	oexit.SetRet(oi)

	// Wire phi backedges now that all values exist.
	ii.Operands[1] = ii2
	ii.PredBlocks[1] = ib
	oi.Operands[1] = oi2
	oi.PredBlocks[1] = olatch
	return f
}

func TestNestedLoopLICM(t *testing.T) {
	orig := buildNestedLoop()
	if err := orig.Verify(); err != nil {
		t.Fatalf("verify before: %v", err)
	}
	forest := AnalyzeLoops(orig)
	if len(forest.Loops) != 2 {
		t.Fatalf("want 2 natural loops, got %d", len(forest.Loops))
	}
	var outer, inner *Loop
	for _, l := range forest.Loops {
		if l.Header.Name == "outer.header" {
			outer = l
		}
		if l.Header.Name == "inner.header" {
			inner = l
		}
	}
	if outer == nil || inner == nil {
		t.Fatal("missing outer/inner loop")
	}
	if inner.Parent != outer {
		t.Fatalf("inner parent = %v, want outer", inner.Parent)
	}
	if outer.Depth != 1 || inner.Depth != 2 {
		t.Fatalf("depths outer=%d inner=%d", outer.Depth, inner.Depth)
	}

	opt := orig.Clone()
	rep := RunLICM(opt)
	if err := opt.Verify(); err != nil {
		t.Fatalf("verify after LICM: %v", err)
	}
	// The fixture already provides unique preheader blocks (inner.ph,
	// entry for outer); they must be recognized rather than duplicated.
	post := AnalyzeLoops(opt)
	for _, l := range post.Loops {
		ph := preheaderOf(post, l)
		if ph == nil {
			t.Fatalf("loop %s lost its unique preheader", l.Header.Name)
		}
		if len(Preds(l.Header)) != len(l.BackEdges)+1 {
			t.Fatalf("header %s must have exactly one outside predecessor",
				l.Header.Name)
		}
	}
	if rep.PreheadersMade > 0 {
		t.Fatalf("existing preheaders should be reused, made=%d",
			rep.PreheadersMade)
	}
	inv := findInstr(opt, OpAdd, 0) // base + k1 must move out of inner.body
	if strings.Contains(inv.Block.Name, "inner.body") {
		t.Fatalf("invariant add still in %s", inv.Block.Name)
	}
	if !isPreheaderOf(post, inv.Block) {
		t.Fatalf("invariant add hoisted to %s, want a loop preheader",
			inv.Block.Name)
	}
	if len(rep.BlockedIrred) != 0 {
		t.Fatalf("reducible loops must not be flagged irreducible: %d", len(rep.BlockedIrred))
	}

	mach := NewMachine()
	if err := AssertEquivalent(mach, orig, opt); err != nil {
		t.Fatal(err)
	}
}

func isPreheaderOf(forest *LoopForest, b *Block) bool {
	for _, l := range forest.Loops {
		if ph := preheaderOf(forest, l); ph == b {
			return true
		}
	}
	return false
}

// TestPreheaderInsertion forces a header with two outside predecessors
// so EnsurePreheaders must synthesize a fresh preheader and rewire phis.
func buildPreIns() *Func {
	f := NewFunc("preins")
	entry := f.NewBlock("entry")
	side := f.NewBlock("side")
	join := f.NewBlock("join")
	alt := f.NewBlock("alt")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	one := entry.EmitConst("one", I32, 1)
	two := side.EmitConst("two", I32, 2)
	zero := entry.EmitConst("zero", I32, 0)
	three := entry.EmitConst("three", I32, 3)
	sel := entry.EmitICmp(PredEQ, zero, one)
	entry.SetCondBr(sel, join, side)
	side.SetBr(join)
	inp := join.EmitPhi(I32, []PhiEdge{{entry, one}, {side, two}})
	joinSel := join.EmitICmp(PredEQ, inp, two)
	altVal := alt.EmitAdd(I32, inp, one)
	_ = altVal
	alt.SetBr(header)
	join.SetCondBr(joinSel, header, alt) // two distinct outside preds into header

	iv := header.EmitPhi(I32, []PhiEdge{
		{join, inp}, {alt, altVal}, {body, nil},
	})
	cmp := header.EmitICmp(PredULT, iv, three)
	header.SetCondBr(cmp, body, exit)
	next := body.EmitAdd(I32, iv, one)
	body.SetBr(header)
	exit.SetRet(iv)
	iv.Operands[2] = next
	iv.PredBlocks[2] = body
	return f
}

func TestPreheaderInsertion(t *testing.T) {
	f := buildPreIns()
	if err := f.Verify(); err != nil {
		t.Fatal(err)
	}
	opt := f.Clone()
	rep := RunLICM(opt)
	if rep.PreheadersMade != 1 {
		t.Fatalf("want exactly 1 synthesized preheader, got %d",
			rep.PreheadersMade)
	}
	if err := opt.Verify(); err != nil {
		t.Fatalf("phi rewiring broke SSA: %v", err)
	}
	hdr := blockNamed(opt, "header")
	outs := 0
	for _, p := range Preds(hdr) {
		if p.Name != "body" {
			outs++
			if !strings.HasSuffix(p.Name, ".preheader") {
				t.Fatalf("outside predecessor must be synthesized preheader, got %s",
					p.Name)
			}
		}
	}
	if outs != 1 {
		t.Fatalf("header must have one outside pred, got %d", outs)
	}
	mach := NewMachine()
	if err := AssertEquivalent(mach, f, opt); err != nil {
		t.Fatal(err)
	}
}

// buildIrreducible builds a classic two-entry irreducible region:
// entry -> A; A branches to B or C; B <-> C form an SCC entered from
// both nodes, then converge at exit.
func buildIrreducible() *Func {
	f := NewFunc("irred")
	entry := f.NewBlock("entry")
	a := f.NewBlock("a")
	b := f.NewBlock("b")
	c := f.NewBlock("c")
	exit := f.NewBlock("exit")

	one := entry.EmitConst("one", I32, 1)
	zero := entry.EmitConst("zero", I32, 0)
	entry.SetBr(a)

	sel := a.EmitICmp(PredEQ, one, zero)
	a.SetCondBr(sel, b, c)

	bv := b.EmitAdd(I32, one, one)
	b.SetBr(c) // B -> C always; C -> B creates the second entry into B's SCC

	cv := c.EmitAdd(I32, bv, one)
	back := c.EmitICmp(PredEQ, cv, zero)
	c.SetCondBr(back, b, exit)

	exit.SetRet(cv)
	return f
}

func TestIrreducibleInterception(t *testing.T) {
	f := buildIrreducible()
	if err := f.Verify(); err != nil {
		t.Fatal(err)
	}
	forest := AnalyzeLoops(f)
	if len(forest.IrreducibleSCCs()) == 0 {
		t.Fatal("want at least one irreducible SCC")
	}
	scc := forest.IrreducibleSCCs()[0]
	member := map[string]bool{}
	for _, n := range scc.Nodes {
		member[n.Name] = true
	}
	if !member["b"] || !member["c"] {
		t.Fatalf("irreducible SCC must contain b and c, got %v", member)
	}
	if len(forest.Loops) != 0 {
		t.Fatalf("irreducible region must not yield natural loops, got %d",
			len(forest.Loops))
	}
	if !forest.IsIrreducibleBlock(blockNamed(f, "b")) {
		t.Fatal("block b must be marked irreducible")
	}

	before := len(blockNamed(f, "b").Instrs)
	rep := RunLICM(f.Clone())
	if len(rep.SkippedSCCs) == 0 {
		t.Fatal("LICM must report skipped irreducible SCC region")
	}
	if len(rep.Hoisted) != 0 {
		t.Fatalf("nothing may be hoisted from irreducible region, got %d",
			len(rep.Hoisted))
	}
	if len(blockNamed(f, "b").Instrs) != before {
		t.Fatal("original function mutated unexpectedly")
	}
}

// buildTrapLoop contains:
//   - safeDiv = x / safeY in the unconditional body, which dominates the
//     only loop exit block (executed whenever the loop is entered),
//   - risky  = x / zeroY inside a conditional block that does not
//     dominate the exit, so hoisting it would create a ghost div-by-zero.
func buildTrapLoop() *Func {
	f := NewFunc("traploop")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	divok := f.NewBlock("divok")
	merge := f.NewBlock("merge")
	exit := f.NewBlock("exit")

	x := entry.EmitConst("x", I32, 20)
	safeY := entry.EmitConst("safeY", I32, 4)
	zeroY := entry.EmitConst("zeroY", I32, 0) // ghost divisor on cold path
	zero := entry.EmitConst("zero", I32, 0)
	one := entry.EmitConst("one", I32, 1)
	limit := entry.EmitConst("limit", I32, 2)
	entry.SetBr(header)

	iv := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {merge, nil}})
	cmp := header.EmitICmp(PredULT, iv, limit)
	// safeDiv lives in the header: it dominates every exit block and is
	// guaranteed to run on each in-loop iteration, so gate (b) passes.
	safeDiv := header.EmitSDiv(I32, x, safeY)
	header.SetCondBr(cmp, body, exit)

	flag := body.EmitICmp(PredEQ, zeroY, one) // always false in this fixture
	body.SetCondBr(flag, divok, merge)

	risky := divok.EmitSDiv(I32, x, zeroY) // traps if the cold path runs
	divok.SetBr(merge)

	joined := merge.EmitPhi(I32, []PhiEdge{{body, safeDiv}, {divok, risky}})
	guard := merge.EmitAdd(I32, joined, one)
	next := merge.EmitAdd(I32, iv, guard)
	merge.SetBr(header)
	exit.SetRet(iv)

	iv.Operands[1] = next
	iv.PredBlocks[1] = merge
	return f
}

func TestTrapGate(t *testing.T) {
	orig := buildTrapLoop()
	if err := orig.Verify(); err != nil {
		t.Fatal(err)
	}
	opt := orig.Clone()
	rep := RunLICM(opt)

	safeDiv := findInstr(opt, OpSDiv, 0)
	risky := findInstr(opt, OpSDiv, 1)
	// The conditional division must be blocked by the trap gate.
	foundBlocked := false
	for _, b := range rep.BlockedTrap {
		if b == risky {
			foundBlocked = true
		}
	}
	if !foundBlocked {
		t.Fatal("conditional x/y must be blocked from hoisting (ghost div-by-zero)")
	}
	postForest := rep.Forest
	if !isPreheaderOf(postForest, safeDiv.Block) {
		t.Fatalf("dominating safe division should hoist to a preheader, got %s",
			safeDiv.Block.Name)
	}
	if isPreheaderOf(postForest, risky.Block) {
		t.Fatal("non-dominating conditional division must stay in loop body")
	}

	mach := NewMachine()
	// y == 0 and flag == 0: risky path never executes, so no trap must
	// appear after optimization either.
	r1 := mach.Run(orig)
	r2 := mach.Run(opt)
	if r1.Trap != nil || r2.Trap != nil {
		t.Fatalf("skipped conditional div must not trap: orig=%+v opt=%+v",
			r1.Trap, r2.Trap)
	}
	if err := AssertEquivalent(mach, orig, opt); err != nil {
		t.Fatal(err)
	}
}

// buildAliasLoop builds a loop loading an invariant address. When
// withAliasStore is true, the body also stores through the same GEP,
// which must suppress load hoisting.
func buildAliasLoop(withAliasStore bool) *Func {
	f := NewFunc("aliasloop")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	latch := f.NewBlock("latch")
	exit := f.NewBlock("exit")

	slot := entry.EmitAlloca("slot", I32, 4)
	addr := entry.EmitGEP(slot, 0)
	nine := entry.EmitConst("nine", I32, 9)
	entry.EmitStore(addr, nine)
	zero := entry.EmitConst("zero", I32, 0)
	one := entry.EmitConst("one", I32, 1)
	limit := entry.EmitConst("limit", I32, 3)
	entry.SetBr(header)

	iv := header.EmitPhi(I32, []PhiEdge{{entry, zero}, {latch, nil}})
	load := header.EmitLoad(I32, addr)
	if withAliasStore {
		plus := header.EmitAdd(I32, load, one)
		header.EmitStore(addr, plus)
	}
	keep := header.EmitAdd(I32, load, one)
	_ = keep
	cmp := header.EmitICmp(PredULT, iv, limit)
	header.SetCondBr(cmp, body, exit)
	body.SetBr(latch)
	next := latch.EmitAdd(I32, iv, one)
	latch.SetBr(header)
	exit.SetRet(iv)

	iv.Operands[1] = next
	iv.PredBlocks[1] = latch
	return f
}

func TestAliasGate(t *testing.T) {
	// No aliasing store: invariant load may hoist (after gate checks).
	clean := buildAliasLoop(false)
	cleanOpt := clean.Clone()
	RunLICM(cleanOpt)
	loadClean := findInstr(cleanOpt, OpLoad, 0)
	cleanForest := AnalyzeLoops(cleanOpt)
	if !isPreheaderOf(cleanForest, loadClean.Block) {
		t.Fatalf("unaliased invariant load should hoist to a preheader, block=%s",
			loadClean.Block.Name)
	}
	mach := NewMachine()
	if err := AssertEquivalent(mach, clean, cleanOpt); err != nil {
		t.Fatal(err)
	}

	// Aliasing store in the loop body: load must remain in the body.
	aliased := buildAliasLoop(true)
	aliasedOpt := aliased.Clone()
	repAliased := RunLICM(aliasedOpt)
	loadAliased := findInstr(aliasedOpt, OpLoad, 0)
	if loadAliased.Block.Name != "header" {
		t.Fatalf("aliased load must stay in header, got %s", loadAliased.Block.Name)
	}
	blocked := false
	for _, b := range repAliased.BlockedAlias {
		if b == loadAliased {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("aliased load must be reported as blocked")
	}
	if err := AssertEquivalent(mach, aliased, aliasedOpt); err != nil {
		t.Fatal(err)
	}
}
