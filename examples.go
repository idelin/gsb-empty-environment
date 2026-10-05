package sccp

// Example programs exercising every feature of the analysis.
// All have at most two 8-bit inputs so CheckExhaustive stays cheap
// (at most 256*256 input combinations).

// ExWrapFold folds 200 + 100 with 8-bit wrap-around to 44.
func ExWrapFold() *Func {
	return &Func{
		Name: "wrap-fold", NumInputs: 1, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "a", Op: OpInput, InputIdx: 0},
				{Dst: "c0", Op: OpConst, Imm: 200},
				{Dst: "c1", Op: OpConst, Imm: 100},
				{Dst: "s", Op: OpAdd, A: "c0", B: "c1"},
				{Dst: "r", Op: OpAdd, A: "s", B: "a"},
			}, Term: Term{Kind: TermRet, RetVar: "r"}},
		},
	}
}

// ExConstBranch has a branch on a provably constant-zero condition:
// edge b0->b1 is dead and block b1 is unreachable.
func ExConstBranch() *Func {
	return &Func{
		Name: "const-branch", NumInputs: 1, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "x", Op: OpInput, InputIdx: 0},
				{Dst: "t", Op: OpConst, Imm: 5},
				{Dst: "z", Op: OpSub, A: "t", B: "t"},
			}, Term: Term{Kind: TermBr, Cond: "z", ThenBlk: 1, ElseBlk: 2}},
			{ID: 1, Insts: []Inst{
				{Dst: "y", Op: OpConst, Imm: 1},
				{Dst: "r", Op: OpAdd, A: "x", B: "y"},
			}, Term: Term{Kind: TermRet, RetVar: "r"}},
			{ID: 2, Insts: []Inst{
				{Dst: "y", Op: OpConst, Imm: 2},
				{Dst: "r", Op: OpAdd, A: "x", B: "y"},
			}, Term: Term{Kind: TermRet, RetVar: "r"}},
		},
	}
}

// ExLoop sums 0..n-1 modulo 256. Loop-carried values must climb the
// lattice monotonically (Const -> Over) and the analysis must terminate.
func ExLoop() *Func {
	return &Func{
		Name: "loop-sum", NumInputs: 1, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "n", Op: OpInput, InputIdx: 0},
				{Dst: "one", Op: OpConst, Imm: 1},
				{Dst: "i", Op: OpConst, Imm: 0},
				{Dst: "s", Op: OpConst, Imm: 0},
			}, Term: Term{Kind: TermJump, Next: 1}},
			{ID: 1, Insts: []Inst{
				{Dst: "c", Op: OpLt, A: "i", B: "n"},
			}, Term: Term{Kind: TermBr, Cond: "c", ThenBlk: 2, ElseBlk: 3}},
			{ID: 2, Insts: []Inst{
				{Dst: "s", Op: OpAdd, A: "s", B: "i"},
				{Dst: "i", Op: OpAdd, A: "i", B: "one"},
			}, Term: Term{Kind: TermJump, Next: 1}},
			{ID: 3, Term: Term{Kind: TermRet, RetVar: "s"}},
		},
	}
}

// ExDivTrap: block b1 divides by a provably-zero constant, so it always
// traps and edge b1->b3 is dead. For input a == 0 the program returns 7.
func ExDivTrap() *Func {
	return &Func{
		Name: "div-trap", NumInputs: 1, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "a", Op: OpInput, InputIdx: 0},
				{Dst: "z", Op: OpConst, Imm: 5},
				{Dst: "d", Op: OpSub, A: "z", B: "z"},
			}, Term: Term{Kind: TermBr, Cond: "a", ThenBlk: 1, ElseBlk: 2}},
			{ID: 1, Insts: []Inst{
				{Dst: "out", Op: OpDiv, A: "a", B: "d"},
			}, Term: Term{Kind: TermJump, Next: 3}},
			{ID: 2, Insts: []Inst{
				{Dst: "out", Op: OpConst, Imm: 7},
			}, Term: Term{Kind: TermJump, Next: 3}},
			{ID: 3, Term: Term{Kind: TermRet, RetVar: "out"}},
		},
	}
}

// ExGuardedDiv divides by an input that is merely non-zero on the taken
// path (path-insensitive analysis sees it as Over). Nothing may be
// folded, cut, or reported as trapping here.
func ExGuardedDiv() *Func {
	return &Func{
		Name: "guarded-div", NumInputs: 2, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "a", Op: OpInput, InputIdx: 0},
				{Dst: "b", Op: OpInput, InputIdx: 1},
			}, Term: Term{Kind: TermBr, Cond: "b", ThenBlk: 1, ElseBlk: 2}},
			{ID: 1, Insts: []Inst{
				{Dst: "out", Op: OpDiv, A: "a", B: "b"},
			}, Term: Term{Kind: TermJump, Next: 3}},
			{ID: 2, Insts: []Inst{
				{Dst: "out", Op: OpConst, Imm: 0},
			}, Term: Term{Kind: TermJump, Next: 3}},
			{ID: 3, Term: Term{Kind: TermRet, RetVar: "out"}},
		},
	}
}

// ExMergeReachable: block b3 has two predecessors, but b2 is
// unreachable, so the join must see only b1's constant 4 and fold w to 8.
func ExMergeReachable() *Func {
	return &Func{
		Name: "merge-reachable", NumInputs: 0, Entry: 0,
		Blocks: []*Block{
			{ID: 0, Insts: []Inst{
				{Dst: "z", Op: OpConst, Imm: 1},
			}, Term: Term{Kind: TermBr, Cond: "z", ThenBlk: 1, ElseBlk: 2}},
			{ID: 1, Insts: []Inst{
				{Dst: "v", Op: OpConst, Imm: 4},
			}, Term: Term{Kind: TermJump, Next: 3}},
			{ID: 2, Insts: []Inst{
				{Dst: "v", Op: OpConst, Imm: 9},
			}, Term: Term{Kind: TermJump, Next: 3}},
			{ID: 3, Insts: []Inst{
				{Dst: "w", Op: OpAdd, A: "v", B: "v"},
			}, Term: Term{Kind: TermRet, RetVar: "w"}},
		},
	}
}

// Examples lists all demo programs.
func Examples() []*Func {
	return []*Func{
		ExWrapFold(), ExConstBranch(), ExLoop(),
		ExDivTrap(), ExGuardedDiv(), ExMergeReachable(),
	}
}
