package netsec

import "testing"

func TestIntervalOps(t *testing.T) {
	if !Bounded(1, 3).SubsetOf(Bounded(0, 4)) {
		t.Fatal("子集判断错误")
	}
	if Bounded(1, 5).SubsetOf(Bounded(2, 4)) {
		t.Fatal("非子集被误判")
	}
	if got := Bounded(1, 3).Hull(Bounded(8, 10)); got.Lo != 1 || got.Hi != 10 {
		t.Fatalf("包络错误: %+v", got)
	}
	if got := Bounded(1, 5).Intersect(Bounded(4, 9)); got.Lo != 4 || got.Hi != 5 {
		t.Fatalf("交集错误: %+v", got)
	}
	if !Bounded(1, 2).Intersect(Bounded(3, 4)).IsEmpty() {
		t.Fatal("无交集应为空区间")
	}
	if got := Bounded(1, 2).Shift(10); got.Lo != 11 || got.Hi != 12 {
		t.Fatalf("平移错误: %+v", got)
	}
	// 溢出平移退化为顶元素，保证可靠。
	if got := Bounded(1, 2).Shift(maxInt); !got.IsTop() {
		t.Fatalf("溢出应退化顶元素: %+v", got)
	}
	// 顶元素加法仍为顶。
	if got := TopInterval().Add(Point(1)); !got.IsTop() {
		t.Fatalf("顶元素加常数应为顶: %+v", got)
	}
}

func TestAffineJoinAndLeq(t *testing.T) {
	a := RegionBase(0).AddConst(2)
	b := RegionBase(1).AddConst(5)
	j := a.Join(b)
	if len(j.Origins) != 2 {
		t.Fatalf("合流来源应为并集: %+v", j.Origins)
	}
	inv := InvariantRegion(0, Bounded(0, 10))
	if !RegionBase(0).AddConst(3).Leq(inv) {
		t.Fatal("偏移 3 应被不变量 [0,10] 容纳")
	}
	if RegionBase(0).AddConst(11).Leq(inv) {
		t.Fatal("偏移 11 不应被不变量 [0,10] 容纳")
	}
}

func TestValidateRejectsBadProgram(t *testing.T) {
	bad := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 0}},
		Blocks:  []Block{{Term: retTerm()}},
	}
	if _, err := Analyze(bad); err == nil {
		t.Fatal("区域大小为 0 应校验失败")
	}

	// 输入缺少有限域声明。
	bad2 := &Program{
		Regions: []Region{{Name: "m", Base: 0, Size: 8}},
		Inputs:  []Input{{Name: "n"}},
		Blocks:  []Block{{Term: retTerm()}},
	}
	if _, err := Analyze(bad2); err == nil {
		t.Fatal("空输入域应校验失败")
	}
}
