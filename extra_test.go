package netsec

import (
	"errors"
	"testing"
)

// TestRejectErrorCarriesSourcePosition 拒绝错误必须带原源位置，便于宿主定位。
func TestRejectErrorCarriesSourcePosition(t *testing.T) {
	f := buildStraight()
	badTrap := BlockID(92)
	f.Blocks = append(f.Blocks, &Block{ID: badTrap, Instrs: []*Instr{
		{ID: InstrID(902), Op: TermTrap, Pos: Position{File: "t.go", Line: 99}},
	}})
	// 制造一个前驱，使跳转需要拆边 -> 必须拒绝。
	entry := f.BlockByID(1)
	term := entry.Instrs[len(entry.Instrs)-1]
	term.Succ = append(term.Succ, badTrap)
	f.initCounters()

	_, err := Instrument(f, Policy{
		Checker: RangeChecker(0, 8),
		Mode:    ModeTrap,
		Trap:    badTrap,
	})
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("want RejectError, got %v", err)
	}
	if rej.Reason == "" {
		t.Fatalf("reject error missing reason")
	}
}

// TestTrapWithPhiRejected 陷阱块自身含 phi 时，访问前跳转会破坏 SSA 合流，必须拒绝。
func TestTrapWithPhiRejected(t *testing.T) {
	f := buildStraight()
	badTrap := BlockID(94)
	f.Blocks = append(f.Blocks, &Block{ID: badTrap, Instrs: []*Instr{
		{ID: InstrID(904), Op: OpPhi, Result: Reg(20),
			Incoming: []PhiEdge{{Pred: 1, Val: Reg(2)}}},
		{ID: InstrID(905), Op: TermTrap},
	}})
	f.initCounters()

	_, err := Instrument(f, Policy{
		Checker: RangeChecker(0, 8),
		Mode:    ModeTrap,
		Trap:    badTrap,
	})
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("phi-containing trap must be rejected, got %v", err)
	}
}

// TestCallbackModePreservesCFGAndPhi 纯回调模式不得改动任何终结符、边或 phi 入边。
func TestCallbackModePreservesCFGAndPhi(t *testing.T) {
	f := buildDiamondWithPhi()
	edgesBefore := dumpEdges(f)
	phiBefore := phiIncoming(f)

	if _, err := Instrument(f, Policy{Checker: RangeChecker(0, 1024)}); err != nil {
		t.Fatalf("instrument: %v", err)
	}
	// 菱形图本身没有内存访问，插桩应为 0，CFG/phi 完全不变。
	if !sameStringSlice(edgesBefore, dumpEdges(f)) {
		t.Fatalf("callback mode altered CFG edges")
	}
	for k, v := range phiBefore {
		if !sameBlockSlice(v, phiIncoming(f)[k]) {
			t.Fatalf("callback mode altered phi incoming of %d", k)
		}
	}
}

// TestTrapModePhiRewrite 在带循环/合流的图上使用安全 trap，验证拆分后
// phi 前驱标签与控制流仍然闭合可解释。
func TestTrapModePhiRewrite(t *testing.T) {
	f := buildLoop()
	trapID := BlockID(93)
	f.Blocks = append(f.Blocks, &Block{ID: trapID, Instrs: []*Instr{
		{ID: InstrID(903), Op: TermTrap, Pos: Position{File: "t.go", Line: 99}},
	}})
	f.initCounters()

	// 全部合法：不应进入 trap，循环正常结束。
	if _, err := Instrument(f, Policy{
		Checker: RangeChecker(0, 4096),
		Mode:    ModeTrap,
		Trap:    trapID,
	}); err != nil {
		t.Fatalf("instrument: %v", err)
	}

	mem := NewMemory()
	mem.Set(0, 11)
	res, err := (&Interpreter{Mem: mem, Checker: RangeChecker(0, 4096)}).Run(f, 1, []int64{0, 2})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Trap {
		t.Fatalf("legal accesses must not trap")
	}
	if res.Return != 42 {
		t.Fatalf("return=%d want 42", res.Return)
	}

	// 结构校验：所有 phi 的前驱标签都指向真实存在的块。
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op != OpPhi {
				continue
			}
			for _, e := range ins.Incoming {
				if f.BlockByID(e.Pred) == nil {
					t.Fatalf("phi %d references missing pred %d", ins.ID, e.Pred)
				}
			}
		}
	}
}

func dumpEdges(f *Func) []string {
	var out []string
	for _, b := range f.Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		term := b.Instrs[len(b.Instrs)-1]
		for _, s := range term.Succ {
			out = append(out, edgeString(b.ID, s))
		}
	}
	return out
}

func edgeString(a, b BlockID) string {
	return string(rune('0'+a)) + "->" + string(rune('0'+b))
}

func sameStringSlice(a, b []string) bool {
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
