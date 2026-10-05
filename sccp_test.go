package sccp

import "testing"

const stepCap = 100000

func foldsFor(rep *Report, dst string) []ConstFold {
	var out []ConstFold
	for _, f := range rep.Folds {
		if f.Dst == dst {
			out = append(out, f)
		}
	}
	return out
}

func TestWrapFold(t *testing.T) {
	f := ExWrapFold()
	_, rep := Optimize(Analyze(f))
	fs := foldsFor(rep, "s")
	if len(fs) != 1 || fs[0].Value != 44 {
		t.Fatalf("expected s folded to 44 (200+100 mod 256), got %+v", fs)
	}
}

func TestConstBranchCutsEdge(t *testing.T) {
	f := ExConstBranch()
	a := Analyze(f)
	_, rep := Optimize(a)
	if len(rep.EdgeCuts) != 1 || rep.EdgeCuts[0].From != 0 || rep.EdgeCuts[0].To != 1 {
		t.Fatalf("expected single edge cut b0->b1, got %+v", rep.EdgeCuts)
	}
	if len(rep.Removals) != 1 || rep.Removals[0].Block != 1 {
		t.Fatalf("expected block b1 removed, got %+v", rep.Removals)
	}
	if a.Reachable[1] {
		t.Fatal("b1 must be unreachable")
	}
}

func TestLoopConvergesMonotonically(t *testing.T) {
	f := ExLoop()
	a := Analyze(f) // must terminate
	// Loop-carried values end up Over at the loop head.
	if got := a.inState[1]["i"]; got.Kind != LatOver {
		t.Fatalf("i at loop head = %v, want Over", got)
	}
	if got := a.inState[1]["s"]; got.Kind != LatOver {
		t.Fatalf("s at loop head = %v, want Over", got)
	}
	// The constant 1 stays constant everywhere.
	if got := a.inState[1]["one"]; got != (Lat{Kind: LatConst, Val: 1}) {
		t.Fatalf("one at loop head = %v, want const 1", got)
	}
	// All blocks reachable, nothing folded inside the loop body.
	_, rep := Optimize(a)
	for _, fold := range rep.Folds {
		if fold.Block == 2 {
			t.Fatalf("unexpected fold in loop body: %+v", fold)
		}
	}
}

func TestDefiniteDivByZero(t *testing.T) {
	f := ExDivTrap()
	a := Analyze(f)
	if a.TrapAt[1] != 0 {
		t.Fatalf("expected b1 to provably trap at inst 0, got %d", a.TrapAt[1])
	}
	if a.Feasible[1][3] {
		t.Fatal("edge b1->b3 must not be feasible")
	}
	_, rep := Optimize(a)
	found := false
	for _, e := range rep.EdgeCuts {
		if e.From == 1 && e.To == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected edge cut b1->b3 with trap reason, got %+v", rep.EdgeCuts)
	}
}

func TestGuardedDivNoFalsePositives(t *testing.T) {
	f := ExGuardedDiv()
	a := Analyze(f)
	_, rep := Optimize(a)
	if a.TrapAt[1] != -1 {
		t.Fatal("guarded division must not be reported as always trapping")
	}
	if len(rep.EdgeCuts) != 0 {
		t.Fatalf("no edge may be cut (condition is input-dependent), got %+v", rep.EdgeCuts)
	}
	if len(rep.Removals) != 0 {
		t.Fatalf("no block may be removed, got %+v", rep.Removals)
	}
}

func TestMergeOnlyReachablePredecessors(t *testing.T) {
	f := ExMergeReachable()
	a := Analyze(f)
	// b3's entry state must join ONLY b1's exit state (v=4), not b2's (v=9).
	if got := a.inState[3]["v"]; got != (Lat{Kind: LatConst, Val: 4}) {
		t.Fatalf("v at b3 = %v, want const 4 (only reachable predecessor)", got)
	}
	_, rep := Optimize(a)
	fs := foldsFor(rep, "w")
	if len(fs) != 1 || fs[0].Value != 8 {
		t.Fatalf("expected w folded to 8, got %+v", fs)
	}
}

func TestExhaustiveEquivalence(t *testing.T) {
	for _, f := range Examples() {
		opt, _ := Optimize(Analyze(f))
		n, err := CheckExhaustive(f, opt, stepCap)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		t.Logf("%s: %d input combinations verified", f.Name, n)
	}
}

// An unvisited edge is never reported as dead: with an input-dependent
// condition both edges stay, and the report stays empty.
func TestUnvisitedEdgesNotMisjudged(t *testing.T) {
	f := &Func{
		Name: "unknown-cond", NumInputs: 1, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "x", Op: OpInput, InputIdx: 0},
			}, Term: Term{Kind: TermBr, Cond: "x", ThenBlk: 1, ElseBlk: 2}},
			{ID: 1, Insts: []Inst{
				{Dst: "r", Op: OpConst, Imm: 1},
			}, Term: Term{Kind: TermRet, RetVar: "r"}},
			{ID: 2, Insts: []Inst{
				{Dst: "r", Op: OpConst, Imm: 2},
			}, Term: Term{Kind: TermRet, RetVar: "r"}},
		},
	}
	_, rep := Optimize(Analyze(f))
	if len(rep.EdgeCuts) != 0 || len(rep.Removals) != 0 {
		t.Fatalf("input-dependent branch must keep everything, got %+v / %+v",
			rep.EdgeCuts, rep.Removals)
	}
}
