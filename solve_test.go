package main

import (
	"math/big"
	"testing"
)

func mkPiece(t *testing.T, start, end string, num, den []string) *Piece {
	t.Helper()
	s, ok := new(big.Rat).SetString(start)
	if !ok {
		t.Fatalf("bad start %q", start)
	}
	var e *big.Rat
	if end != "" {
		e, ok = new(big.Rat).SetString(end)
		if !ok {
			t.Fatalf("bad end %q", end)
		}
	}
	n, err := PolyFromStrings(num)
	if err != nil {
		t.Fatal(err)
	}
	d := Const(big.NewRat(1, 1))
	if den != nil {
		d, err = PolyFromStrings(den)
		if err != nil {
			t.Fatal(err)
		}
	}
	return &Piece{Start: s, End: e, Num: n, Den: d}
}

func mkPW(t *testing.T, name string, pieces ...*Piece) *Piecewise {
	t.Helper()
	pw, err := NewPiecewise(name, pieces)
	if err != nil {
		t.Fatal(err)
	}
	return pw
}

func solve(t *testing.T, f, g *Piecewise, lo, hi string) *Output {
	t.Helper()
	l, _ := new(big.Rat).SetString(lo)
	h, _ := new(big.Rat).SetString(hi)
	s, err := NewSolver(f, g, l, h, big.NewRat(1, 1000000))
	if err != nil {
		t.Fatal(err)
	}
	out := s.Solve()
	if !out.Verification.AllPassed {
		t.Fatalf("核对未通过: %v", out.Verification.Checks)
	}
	return out
}

// 普通穿越交点：x² = 1 → x = ±1
func TestCrossingRoots(t *testing.T) {
	f := mkPW(t, "f", mkPiece(t, "-2", "", []string{"0", "0", "1"}, nil))
	g := mkPW(t, "g", mkPiece(t, "-2", "", []string{"1"}, nil))
	out := solve(t, f, g, "-2", "2")
	if len(out.Roots) != 2 {
		t.Fatalf("期望 2 个根, 得到 %d: %+v", len(out.Roots), out.Roots)
	}
	if out.Roots[0].Value != "-1" || out.Roots[1].Value != "1" {
		t.Fatalf("根值错误: %+v", out.Roots)
	}
	for _, r := range out.Roots {
		if r.Multiplicity != 1 || r.Contact != "crossing" {
			t.Fatalf("期望单重穿越根: %+v", r)
		}
	}
	if len(out.Coincident) != 0 {
		t.Fatalf("不应有重合区间: %+v", out.Coincident)
	}
}

// 重根切触：x² = 0 → x = 0（二重，切触也算相等）
func TestTangencyDoubleRoot(t *testing.T) {
	f := mkPW(t, "f", mkPiece(t, "-1", "", []string{"0", "0", "1"}, nil))
	g := mkPW(t, "g", mkPiece(t, "-1", "", []string{"0"}, nil))
	out := solve(t, f, g, "-1", "1")
	if len(out.Roots) != 1 || out.Roots[0].Value != "0" {
		t.Fatalf("期望精确根 0: %+v", out.Roots)
	}
	if out.Roots[0].Multiplicity != 2 || out.Roots[0].Contact != "tangency" {
		t.Fatalf("期望二重切触: %+v", out.Roots[0])
	}
}

// 无理切触根：(x²-2)² = 0 → x = √2（区间隔离，偶重）
func TestIrrationalTangency(t *testing.T) {
	// (x²-2)² = x⁴ - 4x² + 4
	f := mkPW(t, "f", mkPiece(t, "0", "", []string{"4", "0", "-4", "0", "1"}, nil))
	g := mkPW(t, "g", mkPiece(t, "0", "", []string{"0"}, nil))
	out := solve(t, f, g, "0", "2")
	if len(out.Roots) != 1 {
		t.Fatalf("期望 1 个根: %+v", out.Roots)
	}
	r := out.Roots[0]
	if r.Value != "" || len(r.Interval) != 2 {
		t.Fatalf("期望区间隔离根: %+v", r)
	}
	if r.Approx < 1.414 || r.Approx > 1.415 {
		t.Fatalf("近似值应接近 √2: %v", r.Approx)
	}
	if r.Multiplicity != 2 || r.Contact != "tangency" {
		t.Fatalf("期望二重切触: %+v", r)
	}
}

// 公共因子构造：f 第二段 (x²-1)/(x-1) 约化后与 g = x+1 完全重合
func TestCoincidentCommonFactor(t *testing.T) {
	f := mkPW(t, "f",
		mkPiece(t, "0", "", []string{"1", "1"}, nil),
		mkPiece(t, "2", "4", []string{"-1", "0", "1"}, []string{"-1", "1"}),
	)
	g := mkPW(t, "g", mkPiece(t, "0", "4", []string{"1", "1"}, nil))
	out := solve(t, f, g, "0", "4")
	if len(out.Roots) != 0 {
		t.Fatalf("不应有孤立根: %+v", out.Roots)
	}
	if len(out.Coincident) != 1 {
		t.Fatalf("期望 1 个极大重合区间: %+v", out.Coincident)
	}
	c := out.Coincident[0]
	if c.Lo != "0" || c.Hi != "4" || !c.LoIncluded || !c.HiIncluded {
		t.Fatalf("重合区间应为 [0,4] 双端包含: %+v", c)
	}
	if len(c.Evidence.StaggeredSamples) == 0 {
		t.Fatal("缺少错位采样证据")
	}
	for _, sc := range c.Evidence.StaggeredSamples {
		if !sc.Equal {
			t.Fatalf("错位采样不相等: %+v", sc)
		}
	}
	// 内部断点 x=2 处右连续取值必须核对
	found := false
	for _, bc := range c.Evidence.BreakpointChecks {
		if bc.At == "2" && bc.Equal {
			found = true
		}
	}
	if !found {
		t.Fatal("缺少断点 x=2 的右连续核对")
	}
}

// 恒零差多项式：f ≡ g，整个区间重合，必须终止而非死循环
func TestIdenticalZeroDifference(t *testing.T) {
	mk := func(name string) *Piecewise {
		return mkPW(t, name, mkPiece(t, "0", "2", []string{"1", "0", "1"}, []string{"-3", "1"}))
	}
	out := solve(t, mk("f"), mk("g"), "0", "2")
	if len(out.Coincident) != 1 || out.Coincident[0].Hi != "2" || !out.Coincident[0].HiIncluded {
		t.Fatalf("期望全区间重合 [0,2]: %+v", out.Coincident)
	}
	if len(out.Roots) != 0 {
		t.Fatalf("不应有孤立根: %+v", out.Roots)
	}
}

// 右连续约定：f 在 [0,1) 为 5，在 [1,3] 为 2x；g = 2x
func TestRightContinuousBreakpoint(t *testing.T) {
	f := mkPW(t, "f",
		mkPiece(t, "0", "", []string{"5"}, nil),
		mkPiece(t, "1", "3", []string{"0", "2"}, nil),
	)
	g := mkPW(t, "g", mkPiece(t, "0", "3", []string{"0", "2"}, nil))
	out := solve(t, f, g, "0", "3")
	if len(out.Roots) != 0 {
		t.Fatalf("不应有孤立根: %+v", out.Roots)
	}
	if len(out.Coincident) != 1 || out.Coincident[0].Lo != "1" || out.Coincident[0].Hi != "3" {
		t.Fatalf("期望重合区间 [1,3]: %+v", out.Coincident)
	}
	// x=1 处右连续取值 f(1)=2=g(1)，应包含左端点
	if !out.Coincident[0].LoIncluded {
		t.Fatal("x=1 应按右连续约定包含")
	}
}

// 末端单独包含：x = 2 在区间 [0,2] 的右端点
func TestEndpointIncluded(t *testing.T) {
	f := mkPW(t, "f", mkPiece(t, "0", "", []string{"0", "1"}, nil))
	g := mkPW(t, "g", mkPiece(t, "0", "", []string{"2"}, nil))
	out := solve(t, f, g, "0", "2")
	if len(out.Roots) != 1 || out.Roots[0].Value != "2" {
		t.Fatalf("期望末端根 x=2: %+v", out.Roots)
	}
}

// 伪根剔除：1/x = 2/x 的差多项式根 x=0 处函数无定义
func TestPoleExcluded(t *testing.T) {
	f := mkPW(t, "f", mkPiece(t, "-1", "1", []string{"1"}, []string{"0", "1"}))
	g := mkPW(t, "g", mkPiece(t, "-1", "1", []string{"2"}, []string{"0", "1"}))
	out := solve(t, f, g, "-1", "1")
	if len(out.Roots) != 0 {
		t.Fatalf("极点不应算交点: %+v", out.Roots)
	}
	if len(out.Excluded) != 1 || out.Excluded[0].Value != "0" {
		t.Fatalf("期望剔除点 0: %+v", out.Excluded)
	}
	if len(out.Poles) != 1 || out.Poles[0].Value != "0" {
		t.Fatalf("期望极点 0: %+v", out.Poles)
	}
}

// 有理函数交点：1/x = x/4 → x = 2（区间隔离）
func TestRationalIntersection(t *testing.T) {
	f := mkPW(t, "f", mkPiece(t, "1", "8", []string{"1"}, []string{"0", "1"}))
	g := mkPW(t, "g", mkPiece(t, "1", "8", []string{"0", "1"}, []string{"4"}))
	out := solve(t, f, g, "1", "8")
	if len(out.Roots) != 1 {
		t.Fatalf("期望 1 个根: %+v", out.Roots)
	}
	r := out.Roots[0]
	if r.Approx < 1.999 || r.Approx > 2.001 {
		t.Fatalf("近似值应接近 2: %v", r.Approx)
	}
	if r.Multiplicity != 1 || r.Contact != "crossing" {
		t.Fatalf("期望单重穿越: %+v", r)
	}
}

// 无定义区间：f 只在 [0,2]，g 只在 [1,3]
func TestUndefinedGaps(t *testing.T) {
	f := mkPW(t, "f", mkPiece(t, "0", "2", []string{"0", "1"}, nil))
	g := mkPW(t, "g", mkPiece(t, "1", "3", []string{"0", "1"}, nil))
	out := solve(t, f, g, "0", "3")
	if len(out.Gaps) != 2 {
		t.Fatalf("期望 2 个空缺区间: %+v", out.Gaps)
	}
	if len(out.Coincident) != 1 || out.Coincident[0].Lo != "1" || out.Coincident[0].Hi != "2" {
		t.Fatalf("期望重合区间 [1,2]: %+v", out.Coincident)
	}
}

// 零多项式防护：Sturm/隔离对零多项式安全返回
func TestZeroPolyGuards(t *testing.T) {
	z := Zero()
	if got := z.CountRoots(big.NewRat(-1, 1), big.NewRat(1, 1)); got != 0 {
		t.Fatalf("零多项式根计数应为 0, 得到 %d", got)
	}
	if roots := IsolateRoots(z, big.NewRat(-1, 1), big.NewRat(1, 1), big.NewRat(1, 100)); roots != nil {
		t.Fatalf("零多项式不应产生隔离根: %+v", roots)
	}
}

// 多根与多段混合：f 为分段抛物线，g 为常数
func TestMixedSegments(t *testing.T) {
	// f: [0,1): x² ；[1,3]: (x-2)² ；g = 1/4
	f := mkPW(t, "f",
		mkPiece(t, "0", "", []string{"0", "0", "1"}, nil),
		mkPiece(t, "1", "3", []string{"4", "-4", "1"}, nil),
	)
	g := mkPW(t, "g", mkPiece(t, "0", "3", []string{"1/4"}, nil))
	out := solve(t, f, g, "0", "3")
	// x² = 1/4 → x = 1/2（在 [0,1)）；(x-2)² = 1/4 → x = 3/2, 5/2
	if len(out.Roots) != 3 {
		t.Fatalf("期望 3 个根: %+v", out.Roots)
	}
	want := map[string]bool{"1/2": false, "3/2": false, "5/2": false}
	for _, r := range out.Roots {
		if _, ok := want[r.Value]; !ok {
			t.Fatalf("意外根 %q", r.Value)
		}
		want[r.Value] = true
	}
	for v, seen := range want {
		if !seen {
			t.Fatalf("缺少根 %s", v)
		}
	}
}
