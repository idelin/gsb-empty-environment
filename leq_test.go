package netsec

import "testing"

func TestLeqTypeMismatchRejected(t *testing.T) {
	addr := RegionBase(0).AddConst(2)
	scalar := Const(2)
	if addr.Leq(scalar) {
		t.Fatal("地址值不应被纯标量不变量容纳")
	}
	if scalar.Leq(addr) {
		t.Fatal("纯标量不应被地址不变量容纳")
	}
	ext := ExternalTop()
	plain := TopIntervalValue()
	if ext.Leq(plain) || plain.Leq(ext) {
		t.Fatal("外部与非外部标量不应互相容纳")
	}
}

func TestJoinDoesNotLoseOrigin(t *testing.T) {
	x := RegionBase(0).AddConst(1)
	y := RegionBase(0).AddConst(9)
	j := x.Join(y)
	got := j.Origins[0]
	if got.Lo != 1 || got.Hi != 9 {
		t.Fatalf("同源合流应取偏移包络 [1,9]，得到 %+v", got)
	}
	// 合流结果必须容纳两侧。
	if !x.Leq(j) || !y.Leq(j) {
		t.Fatal("合流结果应容纳两个分支")
	}
}
