package netsec

import "testing"

func TestBottomAndConst(t *testing.T) {
	if !Bottom().IsBottom() {
		t.Fatal("Bottom 应为不可达")
	}
	c := Const(7)
	if c.IsAddress() || !c.Scalar.IsSingle() || c.Scalar.Lo != 7 {
		t.Fatalf("Const 构造错误: %+v", c)
	}
	// Bottom 合流应得到另一方。
	j := Bottom().Join(Const(3))
	if j.Scalar.Lo != 3 {
		t.Fatalf("Bottom join 错误: %+v", j)
	}
	// Bottom 包含于任意值。
	if !Bottom().Leq(Const(3)) {
		t.Fatal("Bottom 应 ⊑ 任意值")
	}
}

func TestEnumerateMultipleInputs(t *testing.T) {
	prog := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 64}},
		Inputs: []Input{
			{Name: "a", Values: []int64{0, 1}},
			{Name: "b", Values: []int64{2, 3, 4}},
		},
		Blocks: []Block{{Term: retTerm()}},
	}
	combos := EnumerateInputs(prog)
	if len(combos) != 6 {
		t.Fatalf("笛卡尔积应为 6，得到 %d", len(combos))
	}
}

func TestRunStepLimit(t *testing.T) {
	// 无界自跳循环，配合小步数上限应超时返回。
	prog := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 8}},
		Inputs:  []Input{{Name: "x", Values: []int64{0}}},
		Blocks: []Block{
			{Term: jumpTo(0)},
		},
	}
	res, err := RunWithLimit(prog, map[InputID]int64{0: 0}, 5)
	if err == nil || res.OK {
		t.Fatal("超步数应返回失败")
	}
}

func TestBranchIntervalComparators(t *testing.T) {
	src := Bounded(0, 100)
	cases := []struct {
		cmp    uint8
		c      int64
		lo, hi int64
	}{
		{CmpLE, 10, 0, 10},
		{CmpLT, 10, 0, 9},
		{CmpGE, 10, 10, 100},
		{CmpGT, 10, 11, 100},
	}
	for _, tc := range cases {
		iv, ok := branchInterval(src, tc.cmp, tc.c)
		if !ok || iv.Lo != tc.lo || iv.Hi != tc.hi {
			t.Fatalf("比较 %d 收窄错误: %+v ok=%v", tc.cmp, iv, ok)
		}
	}
	// CmpNE 不做收窄。
	if _, ok := branchInterval(src, CmpNE, 10); ok {
		t.Fatal("CmpNE 不应收窄")
	}
	// CmpEQ 不命中返回空区间。
	if iv, _ := branchInterval(Bounded(0, 5), CmpEQ, 9); !iv.IsEmpty() {
		t.Fatal("不命中的等值分支应为空")
	}
}

func TestValidateRejectsBadTermAndJump(t *testing.T) {
	badTerm := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 8}},
		Blocks:  []Block{{Term: Terminator{Kind: 99}}},
	}
	if _, err := Analyze(badTerm); err == nil {
		t.Fatal("非法终结符应校验失败")
	}
	badJump := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 8}},
		Blocks:  []Block{{Term: jumpTo(7)}},
	}
	if _, err := Analyze(badJump); err == nil {
		t.Fatal("越界跳转应校验失败")
	}
	badEntry := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 8}},
		Blocks:  []Block{{Term: retTerm()}},
		Entry:   3,
	}
	if _, err := Analyze(badEntry); err == nil {
		t.Fatal("越界入口应校验失败")
	}
	nilProg := (*Program)(nil)
	if _, err := Analyze(nilProg); err == nil {
		t.Fatal("空程序应校验失败")
	}
}

func TestCrossCheckNilReport(t *testing.T) {
	if _, err := CrossCheck(&Program{}, nil); err == nil {
		t.Fatal("空报告应返回错误")
	}
}

func TestRegionContainsOffsetRejects(t *testing.T) {
	r := Region{Base: 100, Size: 16}
	if r.ContainsOffset(-1, 1) {
		t.Fatal("负偏移应拒绝")
	}
	if r.ContainsOffset(15, 2) {
		t.Fatal("越出上界应拒绝")
	}
	if r.ContainsOffset(0, 0) {
		t.Fatal("零大小应拒绝")
	}
	if !r.ContainsOffset(0, 16) {
		t.Fatal("恰好覆盖应通过")
	}
}
