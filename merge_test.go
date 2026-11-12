package netsec

import "testing"

// TestConfluenceKeepsOrigins：两条路径来自不同区域，合流后来源集合取并集。
func TestConfluenceKeepsOrigins(t *testing.T) {
	prog := &Program{
		Regions: []Region{
			{Name: "a", Base: 0, Size: 128},
			{Name: "b", Base: 1000, Size: 128},
		},
		Inputs: []Input{{Name: "flag", Values: []int64{0, 1}}},
		Blocks: []Block{
			{ // 0：按外部 flag 做区间分支
				Ops:  []Op{{Kind: OpInput, Dst: t0, Input: 0}},
				Term: branch(t0, CmpEQ, 0, 1, 2),
			},
			{ // 1：来源区域 0
				Ops:  []Op{{Kind: OpBase, Dst: t1, Region: 0}},
				Term: jumpTo(3),
			},
			{ // 2：来源区域 1
				Ops:  []Op{{Kind: OpBase, Dst: t1, Region: 1}},
				Term: jumpTo(3),
			},
			{ // 3：合流块，t1 来源集合应为 {0,1}
				Ops: []Op{
					{Kind: OpAddConst, Dst: t2, Src: t1, Const: 4},
					{Kind: OpCopy, Dst: t3, Src: t1}, // 索引复制合流基址，保持零偏移
					ld(t4, t2, t3, IndirectRegion, 8, "ldMerge"),
				},
				Term: retTerm(),
			},
		},
		Entry: 0,
	}

	rep, cross, err := Validate(prog)
	if err != nil {
		t.Fatalf("Validate 错误: %v", err)
	}
	if !rep.OK {
		t.Fatalf("期望通过，违例: %+v", rep.Violations)
	}
	cert := rep.Certificates[0]
	if len(cert.Origins) != 2 {
		t.Fatalf("合流后期望 2 个来源，得到 %d (%v)", len(cert.Origins), cert.Origins)
	}
	// 区域 a: 绝对 [4,11]；区域 b: 绝对 [1004,1011]。
	if cert.AccessLo != 4 || cert.AccessHi != 1011 {
		t.Fatalf("统一访问界错误: [%d,%d]", cert.AccessLo, cert.AccessHi)
	}
	if !cross.OK || cross.Cases != 2 || cross.Checked != 2 {
		t.Fatalf("交叉核对失败: %+v", cross)
	}
}
