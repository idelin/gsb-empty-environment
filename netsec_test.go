package netsec

import (
	"errors"
	"fmt"
	"testing"
)

// countOps 统计某操作码出现次数。
func countOps(f *Func, op Op) int {
	n := 0
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op == op {
				n++
			}
		}
	}
	return n
}

func memIDs(f *Func) map[InstrID]bool {
	m := map[InstrID]bool{}
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op.IsMemory() {
				m[ins.ID] = true
			}
		}
	}
	return m
}

// TestInstrumentInsertsCheckBeforeEveryAccess 验证每条读写前恰好一个纯检测回调，
// 且回调携带原源位置与源节点 ID，默认模式不改变块数、终结符与 phi。
func TestInstrumentInsertsCheckBeforeEveryAccess(t *testing.T) {
	f := buildStraight()
	blocksBefore := len(f.Blocks)
	memBefore := memIDs(f)

	n, err := Instrument(f, Policy{Checker: RangeChecker(0, 64)})
	if err != nil {
		t.Fatalf("instrument: %v", err)
	}
	if n != len(memBefore) {
		t.Fatalf("checks=%d want %d", n, len(memBefore))
	}
	if len(f.Blocks) != blocksBefore {
		t.Fatalf("callback mode must not add blocks: %d -> %d", blocksBefore, len(f.Blocks))
	}

	for _, b := range f.Blocks {
		for i, ins := range b.Instrs {
			if ins.Op != OpCheck {
				continue
			}
			if i+1 >= len(b.Instrs) || !b.Instrs[i+1].Op.IsMemory() {
				t.Fatalf("check %d not immediately before a memory op", ins.ID)
			}
			mem := b.Instrs[i+1]
			if !memBefore[mem.ID] {
				t.Fatalf("check guards unknown memory node %d", mem.ID)
			}
			if ins.Pos != mem.Pos {
				t.Fatalf("check pos=%+v != memory pos=%+v", ins.Pos, mem.Pos)
			}
			if len(ins.Args) != 1 || InstrID(ins.Args[0]) != mem.ID {
				t.Fatalf("check must carry source node id, got %+v", ins.Args)
			}
		}
	}
}

// TestInstrumentIdempotent 验证重复插桩幂等；循环回边不会产生重复检测。
func TestInstrumentIdempotentAndLoopBackedge(t *testing.T) {
	f := buildLoop()
	memCount := countOps(f, OpLoad) + countOps(f, OpStore)

	first, err := Instrument(f, Policy{Checker: RangeChecker(0, 4096)})
	if err != nil {
		t.Fatalf("first instrument: %v", err)
	}
	if first != memCount {
		t.Fatalf("first checks=%d want %d", first, memCount)
	}

	// 第二次插桩不得新增任何 check。
	second, err := Instrument(f, Policy{Checker: RangeChecker(0, 4096)})
	if err != nil {
		t.Fatalf("second instrument: %v", err)
	}
	if second != 0 {
		t.Fatalf("re-instrument added %d duplicate checks", second)
	}
	if countOps(f, OpCheck) != memCount {
		t.Fatalf("duplicate checks on loop backedge")
	}

	// 解释执行三轮循环，每条访问仍只触发一次 check/迭代，顺序稳定。
	mem := NewMemory()
	mem.Set(0, 7)
	it := &Interpreter{Mem: mem, Checker: RangeChecker(0, 4096)}
	res, err := it.Run(f, 1, []int64{0, 3})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	checks, reads, writes := 0, 0, 0
	for _, e := range res.Events {
		switch e.Kind {
		case EvCheck:
			checks++
		case EvRead:
			reads++
		case EvWrite:
			writes++
		}
	}
	if checks != 6 || reads != 3 || writes != 3 {
		t.Fatalf("per-iteration events wrong: check=%d read=%d write=%d", checks, reads, writes)
	}
	if res.Return != 42 {
		t.Fatalf("return=%d want 42", res.Return)
	}
}

// TestRejectedWriteHasNoSideEffect 验证被拒写入在访问前中止，内存零改动，
// 且原图与插桩图的异常顺序、返回路径一致。
func TestRejectedWriteHasNoSideEffect(t *testing.T) {
	base := buildStraight()
	inst := Clone(base)

	// 允许读、拒绝写区间：store 地址 16 落在禁止区。
	readAllow := CheckerFunc(func(a Access) error {
		if a.Kind == KindWrite {
			return fmt.Errorf("write denied at %d", a.Addr)
		}
		return RangeChecker(0, 64).Check(a)
	})
	if _, err := Instrument(inst, Policy{Checker: readAllow}); err != nil {
		t.Fatalf("instrument: %v", err)
	}

	mem0 := NewMemory()
	mem0.Set(0, 9)

	baseIt := &Interpreter{Mem: mem0, Checker: readAllow}
	baseRes, err := baseIt.Run(base, 1, []int64{})
	if err != nil {
		t.Fatalf("base run: %v", err)
	}

	instIt := &Interpreter{Mem: NewMemory(), Checker: readAllow}
	instIt.Mem.Set(0, 9)
	instRes, err := instIt.Run(inst, 1, []int64{})
	if err != nil {
		t.Fatalf("inst run: %v", err)
	}

	if baseRes.Err == nil || instRes.Err == nil {
		t.Fatalf("both runs must reject the write: base=%v inst=%v", baseRes.Err, instRes.Err)
	}
	rep := CompareExecution(baseRes, instRes)
	if !rep.SequenceEqual {
		t.Fatalf("exception order mismatch: %s", rep.Mismatch)
	}
	if !rep.RejectNoSide {
		t.Fatalf("rejected write produced a side effect")
	}
	// 独立核对：拒绝后内存与初始夹具完全相同。
	if instRes.MemSnapshot[16] != 0 || instRes.MemSnapshot[0] != 9 {
		t.Fatalf("memory mutated by rejected store: %+v", instRes.MemSnapshot)
	}
}

// TestLegalAccessEquivalent 验证合法访问下原图与插桩图返回值与事件顺序一致。
func TestLegalAccessEquivalent(t *testing.T) {
	base := buildStraight()
	inst := Clone(base)
	if _, err := Instrument(inst, Policy{Checker: RangeChecker(0, 64)}); err != nil {
		t.Fatalf("instrument: %v", err)
	}

	mem := NewMemory()
	mem.Set(0, 5)
	mem.Set(16, 1)
	r1, err := (&Interpreter{Mem: mem, Checker: RangeChecker(0, 64)}).Run(base, 1, nil)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	// 第二份内存给插桩图，保证起点一致。
	r2, err := (&Interpreter{Mem: mem.Clone(), Checker: RangeChecker(0, 64)}).Run(inst, 1, nil)
	if err != nil {
		t.Fatalf("inst: %v", err)
	}

	rep := CompareExecution(r1, r2)
	if !rep.ReturnEqual || !rep.SequenceEqual {
		t.Fatalf("legal execution diverged: %+v", rep)
	}
}

// TestTrapModeSafeAndRejected 验证：
// 安全的叶子陷阱块可在访问前跳转；需要拆边/触碰 phi 的请求被 *RejectError 拒绝。
func TestTrapModeSafeAndRejected(t *testing.T) {
	// 安全情形：独立陷阱块，无前驱、无 phi。
	safe := buildStraight()
	trapID := BlockID(90)
	safe.Blocks = append(safe.Blocks, &Block{ID: trapID, Instrs: []*Instr{
		{ID: InstrID(900), Op: TermTrap, Pos: Position{File: "t.go", Line: 99}},
	}})
	safe.initCounters()

	n, err := Instrument(safe, Policy{
		Checker: RangeChecker(0, 8), // 地址 16 的访问会被拒
		Mode:    ModeTrap,
		Trap:    trapID,
	})
	if err != nil {
		t.Fatalf("safe trap insert refused: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected guards")
	}

	mem := NewMemory()
	mem.Set(0, 3)
	res, err := (&Interpreter{Mem: mem, Checker: RangeChecker(0, 8)}).Run(safe, 1, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Trap {
		t.Fatalf("rejected access must reach trap block")
	}
	if res.MemSnapshot[16] != 0 {
		t.Fatalf("trap path must not perform the store")
	}

	// 拒绝情形：陷阱块已有前驱（插入跳转将引入临界边/合流改动）。
	bad := buildStraight()
	badTrap := BlockID(91)
	bad.Blocks = append(bad.Blocks, &Block{ID: badTrap, Instrs: []*Instr{
		{ID: InstrID(901), Op: TermTrap},
	}})
	// 让入口块额外跳向陷阱块，制造一个前驱。
	entry := bad.BlockByID(1)
	entry.Instrs[len(entry.Instrs)-1].Succ = append(
		entry.Instrs[len(entry.Instrs)-1].Succ, badTrap)
	bad.initCounters()
	_, err = Instrument(bad, Policy{Checker: RangeChecker(0, 8), Mode: ModeTrap, Trap: badTrap})
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("want *RejectError, got %v", err)
	}
}

// TestPhiPredecessorRewriteOnSplit 验证块拆分后，后继 phi 的前驱标签被同步修正，
// SSA 合流仍能取到正确值。
func TestPhiPredecessorRewriteOnSplit(t *testing.T) {
	f := buildDiamondWithPhi()
	before := phiIncoming(f)

	// 手工拆分临界边 2->4（4 有两个前驱且含 phi）。
	mid, err := SplitCriticalEdge(f, 2, 4)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if mid == 4 {
		t.Fatalf("edge should be split into a new block")
	}
	after := phiIncoming(f)
	if sameBlockSlice(before[4], after[4]) {
		t.Fatalf("phi predecessor for block 4 not rewritten after split")
	}

	// 解释执行确认合流值正确：走 true 分支。
	mem := NewMemory()
	res, err := (&Interpreter{Mem: mem, Checker: RangeChecker(0, 1024)}).Run(f, 1, nil)
	if err != nil {
		t.Fatalf("run after split: %v", err)
	}
	if res.Return != 7 {
		t.Fatalf("phi join value wrong, return=%d want 7", res.Return)
	}
}

func phiIncoming(f *Func) map[BlockID][]BlockID {
	out := map[BlockID][]BlockID{}
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op == OpPhi {
				for _, e := range ins.Incoming {
					out[b.ID] = append(out[b.ID], e.Pred)
				}
			}
		}
	}
	return out
}

func sameBlockSlice(a, b []BlockID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// buildStraight: entry -> load(0) -> store(16, v) -> return v。
func buildStraight() *Func {
	tb := newBuilder("straight")
	tb.block(1).at(1)
	tb.const_(1, 0)         // addr1
	tb.at(2).load(2, 1, 1)  // r = load(0)
	tb.const_(3, 16)        // addr2
	tb.at(3).store(3, 2, 1) // store(16, r)
	tb.at(4).ret(2)
	return tb.f
}

// buildDiamondWithPhi: entry(cond=1) -> bb2/bb3 -> merge(bb4, phi) -> return phi。
func buildDiamondWithPhi() *Func {
	tb := newBuilder("diamond")
	tb.block(1).at(1)
	tb.const_(1, 1) // cond true
	tb.branch(1, 2, 3)

	tb.block(2).at(2)
	tb.const_(2, 7)
	tb.jump(4)

	tb.block(3).at(3)
	tb.const_(3, 9)
	tb.jump(4)

	tb.block(4).at(4)
	tb.phi(4, PhiEdge{Pred: 2, Val: 2}, PhiEdge{Pred: 3, Val: 3})
	tb.ret(4)
	return tb.f
}
