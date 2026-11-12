package netsec

import "testing"

// TestCheckedExternalIndirect：外部输入经双向边界检查后作为间接读取索引。
func TestCheckedExternalIndirect(t *testing.T) {
	prog := &Program{
		Regions: []Region{{Name: "mem", Base: 100, Size: 32}},
		Inputs:  []Input{{Name: "n", Values: []int64{-5, 0, 31, 32}}},
		Blocks: []Block{
			{ // 0：下界检查
				Ops:  []Op{{Kind: OpInput, Dst: t0, Input: 0}},
				Term: branch(t0, CmpGE, 0, 1, 4),
			},
			{ // 1：上界检查（读 1 字节，允许 0..31）
				Term: branch(t0, CmpLE, 31, 2, 4),
			},
			{ // 2：基址 + 收窄索引的间接读取
				Ops: []Op{
					{Kind: OpBase, Dst: t1, Region: 0},
					ld(t2, t1, t0, IndirectRegion, 1, "ldExt"),
				},
				Term: jumpTo(3),
			},
			{Term: retTerm()}, // 3
			{Term: retTerm()}, // 4 拒绝路径
		},
		Entry: 0,
	}
	rep, cross, err := Validate(prog)
	if err != nil {
		t.Fatalf("Validate 错误: %v", err)
	}
	if !rep.OK {
		t.Fatalf("收窄后的外部索引应通过: %+v", rep.Violations)
	}
	cert := rep.Certificates[0]
	if !cert.IndexRange.SubsetOf(Bounded(0, 31)) {
		t.Fatalf("索引应收窄到 [0,31]: %+v", cert.IndexRange)
	}
	// 绝对访问界 = 区域基址 100 + [0,31]。
	if cert.AccessLo != 100 || cert.AccessHi != 131 {
		t.Fatalf("绝对访问界错误: [%d,%d]", cert.AccessLo, cert.AccessHi)
	}
	if !cross.OK {
		t.Fatalf("交叉核对失败: %+v", cross.Mismatches)
	}
}

// TestPerOriginRanges：合流来源的逐来源绝对区间应分别正确。
func TestPerOriginRanges(t *testing.T) {
	prog := &Program{
		Regions: []Region{
			{Name: "a", Base: 0, Size: 64},
			{Name: "b", Base: 500, Size: 64},
		},
		Inputs: []Input{{Name: "flag", Values: []int64{0, 1}}},
		Blocks: []Block{
			{Ops: []Op{{Kind: OpInput, Dst: t0, Input: 0}}, Term: branch(t0, CmpEQ, 0, 1, 2)},
			{Ops: []Op{{Kind: OpBase, Dst: t1, Region: 0}}, Term: jumpTo(3)},
			{Ops: []Op{{Kind: OpBase, Dst: t1, Region: 1}}, Term: jumpTo(3)},
			{
				Ops: []Op{
					{Kind: OpAddConst, Dst: t2, Src: t1, Const: 2},
					{Kind: OpCopy, Dst: t3, Src: t1},
					ld(t4, t2, t3, IndirectRegion, 4, "ld"),
				},
				Term: retTerm(),
			},
		},
	}
	rep, err := Analyze(prog)
	if err != nil {
		t.Fatalf("Analyze 错误: %v", err)
	}
	if !rep.OK {
		t.Fatalf("应通过: %+v", rep.Violations)
	}
	cert := rep.Certificates[0]
	if got := cert.OriginRanges[0]; got.Lo != 2 || got.Hi != 5 {
		t.Fatalf("区域 a 绝对区间错误: %+v", got)
	}
	if got := cert.OriginRanges[1]; got.Lo != 502 || got.Hi != 505 {
		t.Fatalf("区域 b 绝对区间错误: %+v", got)
	}
}
