package netsec

import "testing"

// TestUncheckedExternalRejected：无边界检查的外部输入寻址必须被拒绝。
func TestUncheckedExternalRejected(t *testing.T) {
	prog := &Program{
		Regions: []Region{{Name: "mem", Base: 0, Size: 16}},
		Inputs:  []Input{{Name: "n", Values: []int64{0, 100}}},
		Blocks: []Block{{
			Ops: []Op{
				{Kind: OpInput, Dst: t0, Input: 0},
				{Kind: OpBase, Dst: t1, Region: 0},
				ld(t2, t1, t0, 0, 1, "ldRaw"),
			},
			Term: retTerm(),
		}},
		Entry: 0,
	}
	rep, err := Analyze(prog)
	if err != nil {
		t.Fatalf("Analyze 错误: %v", err)
	}
	if rep.OK {
		t.Fatalf("未检查的外部输入读取必须被拒绝")
	}
}

// TestExternalBoundCheckNarrowing：只有双向边界检查收窄后才放行，且证书带检查站点。
func TestExternalBoundCheckNarrowing(t *testing.T) {
	// 仅上界检查：n 可能为负，应保守拒绝。
	upperOnly := &Program{
		Regions: []Region{{Name: "mem", Base: 0, Size: 16}},
		Inputs:  []Input{{Name: "n", Values: []int64{-1, 0, 15}}},
		Blocks: []Block{
			{
				Ops:  []Op{{Kind: OpInput, Dst: t0, Input: 0}},
				Term: branch(t0, CmpLE, 15, 1, 2),
			},
			{
				Ops: []Op{
					{Kind: OpBase, Dst: t1, Region: 0},
					ld(t2, t1, t0, 0, 1, "ldChecked"),
				},
				Term: retTerm(),
			},
			{Term: retTerm()},
		},
		Entry: 0,
	}
	if rep, err := Analyze(upperOnly); err != nil {
		t.Fatalf("Analyze 错误: %v", err)
	} else if rep.OK {
		t.Fatalf("仅上界检查不应放行可能为负的索引")
	}

	// 双向检查 n>=0 且 n<=15 后应通过。
	full := &Program{
		Regions: []Region{{Name: "mem", Base: 0, Size: 16}},
		Inputs:  []Input{{Name: "n", Values: []int64{-1, 0, 7, 15, 16}}},
		Blocks: []Block{
			{ // 0：下界检查
				Ops:  []Op{{Kind: OpInput, Dst: t0, Input: 0}},
				Term: branch(t0, CmpGE, 0, 1, 3),
			},
			{ // 1：上界检查
				Term: branch(t0, CmpLE, 15, 2, 3),
			},
			{ // 2：在界内读取
				Ops: []Op{
					{Kind: OpBase, Dst: t1, Region: 0},
					ld(t2, t1, t0, 0, 1, "ldChecked"),
				},
				Term: retTerm(),
			},
			{ // 3：拒绝路径
				Term: retTerm(),
			},
		},
		Entry: 0,
	}
	rep, cross, err := Validate(full)
	if err != nil {
		t.Fatalf("Validate 错误: %v", err)
	}
	if !rep.OK {
		t.Fatalf("双向边界检查后应通过: %+v", rep.Violations)
	}
	if len(rep.Certificates[0].CheckSites) == 0 {
		t.Fatalf("证书应记录边界检查站点")
	}
	if !rep.Certificates[0].IndexRange.SubsetOf(Bounded(0, 15)) {
		t.Fatalf("收窄后的索引区间错误: %+v", rep.Certificates[0].IndexRange)
	}
	if !cross.OK {
		t.Fatalf("交叉核对失败: %+v", cross.Mismatches)
	}
}
