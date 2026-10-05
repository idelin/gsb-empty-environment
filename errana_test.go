package main

import (
	"math/big"
	"testing"
)

func TestSameSourceCancellation(t *testing.T) {
	e := NewEngine()
	memo := map[*Node]*Affine{}
	x := InputNode("x", "2", "-1/8", "1/8")
	a, err := Sub(x, x).Eval(e, memo)
	if err != nil {
		t.Fatal(err)
	}
	r := a.Range()
	if r.Lo.Sign() != 0 || r.Hi.Sign() != 0 {
		t.Fatalf("x - x 应为 [0,0]，得到 %v", r)
	}
}

func TestIndependentSubtractionDoubles(t *testing.T) {
	e := NewEngine()
	memo := map[*Node]*Affine{}
	x := InputNode("x", "2", "-1/8", "1/8")
	x2 := InputNode("x2", "2", "-1/8", "1/8")
	a, err := Sub(x, x2).Eval(e, memo)
	if err != nil {
		t.Fatal(err)
	}
	r := a.Range()
	if r.Lo.Cmp(big.NewRat(-1, 4)) != 0 || r.Hi.Cmp(big.NewRat(1, 4)) != 0 {
		t.Fatalf("x - x2 应为 [-1/4,1/4]，得到 %v", r)
	}
}

func TestDivZeroRejected(t *testing.T) {
	e := NewEngine()
	memo := map[*Node]*Affine{}
	x := InputNode("x", "2", "-1/8", "1/8")
	y := InputNode("y", "3", "-1/4", "1/4")
	if _, err := Div(x, Sub(y, ConstNode("3"))).Eval(e, memo); err == nil {
		t.Fatal("除数区间过零应被拒绝")
	}
}

func TestContainmentSharedExpr(t *testing.T) {
	e := NewEngine()
	memo := map[*Node]*Affine{}
	x := InputNode("x", "2", "-1/8", "1/8")
	y := InputNode("y", "3", "-1/4", "1/4")
	z := InputNode("z", "1", "-1/16", "1/16")
	s := Add(Mul(x, y), z)
	f := Div(Mul(s, s), Add(s, ConstNode("2")))
	af, err := f.Eval(e, memo)
	if err != nil {
		t.Fatal(err)
	}
	res := Verify(f, []*Node{x, y, z}, af.Range())
	if !res.OK {
		t.Fatalf("包含性验证失败: %v", res)
	}
	if res.Samples == 0 {
		t.Fatal("没有有效样本")
	}
}

func TestRecipCorrelation(t *testing.T) {
	e := NewEngine()
	memo := map[*Node]*Affine{}
	x := InputNode("x", "2", "-1/8", "1/8")
	a, err := Div(x, x).Eval(e, memo)
	if err != nil {
		t.Fatal(err)
	}
	r := a.Range()
	// x/x 的界必须包含 1 且宽度为 O(r²)，远小于输入半径 1/8
	if !r.contains(big.NewRat(1, 1)) {
		t.Fatalf("x/x 的界 %v 应包含 1", r)
	}
	width := new(big.Rat).Sub(r.Hi, r.Lo)
	if width.Cmp(big.NewRat(1, 32)) > 0 {
		t.Fatalf("x/x 的界宽 %s 应为 O(r²) 量级", width.RatString())
	}
	if res := Verify(Div(x, x), []*Node{x}, r); !res.OK {
		t.Fatal("x/x 枚举验证失败")
	}
}

func TestAsymmetricErrorInterval(t *testing.T) {
	e := NewEngine()
	memo := map[*Node]*Affine{}
	x := InputNode("x", "2", "-1/8", "3/8") // 不对称误差
	a, err := x.Eval(e, memo)
	if err != nil {
		t.Fatal(err)
	}
	r := a.Range()
	if r.Lo.Cmp(big.NewRat(15, 8)) != 0 || r.Hi.Cmp(big.NewRat(19, 8)) != 0 {
		t.Fatalf("不对称误差区间应为 [15/8,19/8]，得到 %v", r)
	}
}
