package netsec

import "testing"

// 循环程序的块编号。
const (
	lbEntry  BlockID = 0
	lbHeader BlockID = 1
	lbGuard  BlockID = 2
	lbBody   BlockID = 3
	lbExit   BlockID = 4
)

// loopProgram 构造一个计数循环：
//
//	entry: i = 外部 seed；检查 0<=i<=4 后进入 header
//	header: 循环头（宿主给定不变量 i∈[0,4]），跳到 guard
//	guard: 若 i<=3 进入 body，否则 exit
//	body:  读取 mem[i]，i++，回到 header
//	exit:  返回
//
// bad=true 时给定过紧的不变量（i<=1），回边 i=2 破坏保持性。
func loopProgram(bad bool) *Program {
	invHi := int64(4)
	if bad {
		invHi = 1
	}
	return &Program{
		Regions: []Region{{Name: "mem", Base: 0, Size: 16}},
		Inputs:  []Input{{Name: "seed", Values: []int64{0}}},
		Blocks: []Block{
			{ // 0 entry
				Ops: []Op{
					{Kind: OpBase, Dst: t0, Region: 0},
					{Kind: OpInput, Dst: t1, Input: 0},
				},
				Term: branch(t1, CmpGE, 0, 5, lbExit),
			},
			{ // 1 header（不变量所在）
				Term: jumpTo(lbGuard),
			},
			{ // 2 guard：显式上界边界检查
				Term: branch(t1, CmpLE, 3, lbBody, lbExit),
			},
			{ // 3 body：读取 mem[i] 后 i++，回边到 header
				Ops: []Op{
					ld(t2, t0, t1, IndirectRegion, 1, "ldLoop"),
					{Kind: OpAddConst, Dst: t1, Src: t1, Const: 1},
				},
				Term: jumpTo(lbHeader),
			},
			{ // 4 exit
				Term: retTerm(),
			},
			{ // 5：entry 下界检查通过后的上界检查
				Term: branch(t1, CmpLE, 4, lbHeader, lbExit),
			},
		},
		Entry: lbEntry,
		Invariants: map[BlockID]map[RegisterID]Affine{
			lbHeader: {
				t0: RegionBase(0),
				t1: {Scalar: Bounded(0, invHi), External: true},
			},
		},
	}
}

func TestLoopInvariantHolds(t *testing.T) {
	rep, cross, err := Validate(loopProgram(false))
	if err != nil {
		t.Fatalf("Validate 错误: %v", err)
	}
	if !rep.OK {
		t.Fatalf("不变量保持时应通过: %+v", rep.Violations)
	}
	if len(rep.LoopChecks) != 1 || !rep.LoopChecks[0].Holds {
		t.Fatalf("循环检查结论错误: %+v", rep.LoopChecks)
	}
	// 具体执行读取 i=0..3，共 4 次，全部在界内。
	if !cross.OK || cross.Checked != 4 {
		t.Fatalf("交叉核对失败: %+v", cross)
	}
}

func TestLoopInvariantViolation(t *testing.T) {
	rep, err := Analyze(loopProgram(true))
	if err != nil {
		t.Fatalf("Analyze 错误: %v", err)
	}
	if rep.OK {
		t.Fatalf("不变量被破坏时必须报告违例")
	}
	found := false
	for _, lc := range rep.LoopChecks {
		if lc.Header == lbHeader && !lc.Holds {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到循环头的保持性失败: %+v", rep.LoopChecks)
	}
}
