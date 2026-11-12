package netsec

// 测试夹具：用最小构造器快速搭建带源节点 ID 的有限 SSA 图。

type testBuilder struct {
	f      *Func
	blocks map[BlockID]*Block
	pos    Position
}

func newBuilder(name string) *testBuilder {
	return &testBuilder{
		f:      &Func{Name: name},
		blocks: map[BlockID]*Block{},
		pos:    Position{File: "t.go", Line: 1},
	}
}

func (tb *testBuilder) at(line int) *testBuilder {
	tb.pos = Position{File: "t.go", Line: line}
	return tb
}

func (tb *testBuilder) block(id BlockID) *testBuilder {
	b := &Block{ID: id}
	tb.f.Blocks = append(tb.f.Blocks, b)
	tb.blocks[id] = b
	if int(id) > tb.f.nextBlock {
		tb.f.nextBlock = int(id)
	}
	return tb
}

func (tb *testBuilder) param(r Reg) *testBuilder {
	tb.f.Params = append(tb.f.Params, r)
	if int(r) > tb.f.NumRegs {
		tb.f.NumRegs = int(r)
	}
	return tb
}

func (tb *testBuilder) emit(ins *Instr) *Instr {
	b := tb.f.Blocks[len(tb.f.Blocks)-1]
	if ins.ID == 0 {
		tb.f.nextInstr++
		ins.ID = InstrID(tb.f.nextInstr)
	} else if int(ins.ID) > tb.f.nextInstr {
		tb.f.nextInstr = int(ins.ID)
	}
	if ins.Pos == (Position{}) {
		ins.Pos = tb.pos
	}
	if ins.Result != 0 && int(ins.Result) > tb.f.NumRegs {
		tb.f.NumRegs = int(ins.Result)
	}
	b.Instrs = append(b.Instrs, ins)
	return ins
}

func (tb *testBuilder) const_(r Reg, v int64) *testBuilder {
	tb.emit(&Instr{Op: OpConst, Result: r, Val: v})
	return tb
}

func (tb *testBuilder) add(r, a, b Reg) *testBuilder {
	tb.emit(&Instr{Op: OpAdd, Result: r, Args: []ValueID{a, b}})
	return tb
}

func (tb *testBuilder) sub(r, a, b Reg) *testBuilder {
	tb.emit(&Instr{Op: OpSub, Result: r, Args: []ValueID{a, b}})
	return tb
}

func (tb *testBuilder) load(r, addr Reg, size int) *Instr {
	return tb.emit(&Instr{Op: OpLoad, Result: r, Size: size, Args: []ValueID{addr}})
}

func (tb *testBuilder) store(addr, val Reg, size int) *Instr {
	return tb.emit(&Instr{Op: OpStore, Size: size, Args: []ValueID{addr, val}})
}

func (tb *testBuilder) phi(r Reg, edges ...PhiEdge) *testBuilder {
	tb.emit(&Instr{Op: OpPhi, Result: r, Incoming: edges})
	return tb
}

func (tb *testBuilder) jump(to BlockID) *testBuilder {
	tb.emit(&Instr{Op: TermJump, Succ: []BlockID{to}})
	return tb
}

func (tb *testBuilder) branch(cond Reg, t, f BlockID) *testBuilder {
	tb.emit(&Instr{Op: TermBranch, Args: []ValueID{cond}, Succ: []BlockID{t, f}})
	return tb
}

func (tb *testBuilder) ret(r Reg) *testBuilder {
	tb.emit(&Instr{Op: TermReturn, Args: []ValueID{r}})
	return tb
}

func (tb *testBuilder) trap() *testBuilder {
	tb.emit(&Instr{Op: TermTrap})
	return tb
}

// buildLoop 构造带自然循环回边的有限 SSA 图：
//
//	entry(1) -> header(2, phi i)
//	header --cond--> body(3): load; store; i++ -> back header
//	header ---------> exit(4): return 42
//
// 条件 cond = n + (-i)（解释器只判定零/非零），n 次迭代后退出。
func buildLoop() *Func {
	tb := newBuilder("loop")
	tb.param(1) // base
	tb.param(2) // n

	tb.block(1).at(10)
	tb.const_(3, 0) // i0 = 0
	tb.jump(2)

	tb.block(2).at(20)
	tb.phi(4, PhiEdge{Pred: 1, Val: 3}, PhiEdge{Pred: 3, Val: 6}) // i
	tb.sub(7, 2, 4)                                               // cond = n - i
	tb.branch(7, 3, 4)

	tb.block(3).at(30)
	tb.load(5, 1, 4)         // tmp = load(base)
	tb.add(8, 1, 4)          // addr = base + i
	tb.at(31).store(8, 5, 4) // store(addr, tmp)
	tb.const_(9, 1)
	tb.add(6, 4, 9) // i1 = i + 1
	tb.jump(2)

	tb.block(4).at(40)
	tb.const_(10, 42)
	tb.ret(10)

	return tb.f
}
