package reorder

import "testing"

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b Location
		want bool
	}{
		{Reg("r1"), Reg("r1"), true},
		{Reg("r1"), Reg("r2"), false},
		{Reg("r1"), Mem(0, 4), false},
		{Mem(0, 4), Mem(4, 8), false}, // 半开区间端点相接不相交
		{Mem(0, 5), Mem(4, 8), true},  // 重叠一个字节
		{Mem(0, 4), Mem(0, 4), true},
		{Mem(2, 2), Mem(0, 10), true}, // 包含
		{Mem(8, 4), Mem(0, 4), false}, // 不相交
	}
	for i, c := range cases {
		if got := c.a.Overlaps(c.b); got != c.want {
			t.Errorf("用例 %d: %v.Overlaps(%v) = %v，期望 %v", i, c.a, c.b, got, c.want)
		}
		if got := c.b.Overlaps(c.a); got != c.want {
			t.Errorf("用例 %d（对称）: %v.Overlaps(%v) = %v，期望 %v", i, c.b, c.a, got, c.want)
		}
	}
}

func mustParse(t *testing.T, src string) []Inst {
	t.Helper()
	insts, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	return insts
}

func findEdge(edges []Edge, from, to int, kind DepKind) *Edge {
	for i := range edges {
		if edges[i].From == from && edges[i].To == to && edges[i].Kind == kind {
			return &edges[i]
		}
	}
	return nil
}

func TestComputeEdgesKinds(t *testing.T) {
	insts := mustParse(t, `
r1 = li 1
r2 = mov r1
r1 = li 2
`)
	edges := ComputeEdges(insts)
	if findEdge(edges, 0, 1, DepRAW) == nil {
		t.Error("缺少 RAW 边 #1->#2（r1）")
	}
	if findEdge(edges, 1, 2, DepWAR) == nil {
		t.Error("缺少 WAR 边 #2->#3（r1）")
	}
	if findEdge(edges, 0, 2, DepWAW) == nil {
		t.Error("缺少 WAW 边 #1->#3（r1）")
	}
}

func TestComputeEdgesMemAlias(t *testing.T) {
	// 部分重叠的内存区间必须精确判别名
	insts := mustParse(t, `
r1 = load [0, 4)
[2, 6) = store r1
`)
	edges := ComputeEdges(insts)
	if findEdge(edges, 0, 1, DepRAW) == nil {
		t.Error("缺少 RAW 边（r1）")
	}
	war := findEdge(edges, 0, 1, DepWAR)
	if war == nil {
		t.Error("缺少 WAR 边：[0,4) 与 [2,6) 相交")
	} else if war.Loc != Mem(0, 4) {
		t.Errorf("WAR 边冲突位置 = %v，期望 [0, 4)", war.Loc)
	}

	// 端点相接的半开区间不相交，不应有边
	insts2 := mustParse(t, `
r1 = load [0, 4)
r2 = load [4, 8)
[8, 12) = store r1
`)
	for _, e := range ComputeEdges(insts2) {
		if e.Kind == DepWAR || e.Kind == DepWAW {
			t.Errorf("不相交区间产生了多余的边: %s", e)
		}
	}
}

func TestComputeEdgesBarrier(t *testing.T) {
	insts := mustParse(t, `
r1 = li 1
barrier
r2 = li 2
`)
	edges := ComputeEdges(insts)
	if findEdge(edges, 0, 1, DepBarrier) == nil {
		t.Error("缺少 屏障前 -> 屏障 边")
	}
	if findEdge(edges, 1, 2, DepBarrier) == nil {
		t.Error("缺少 屏障 -> 屏障后 边")
	}
	// 跨屏障顺序由 屏障前->屏障 与 屏障->屏障后 两条边传递保证，
	// 不需要直接边。
	if findEdge(edges, 0, 2, DepBarrier) != nil {
		t.Error("不应产生跨屏障直接边（由传递性覆盖）")
	}
}
