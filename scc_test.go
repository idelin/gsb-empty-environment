package scc

import (
	"reflect"
	"testing"
)

// 验收场景：三角形环 A->B->C->A 合并为一个分量；
// 加入独立节点 D 及边 C->D；删除边 B->C 后，
// 原分量精准分裂为 A、B、C 三个单节点分量，D 的归属不受影响。
func TestAcceptance_TriangleMergeThenSplit(t *testing.T) {
	tr := NewTracker()

	// 逐步构建三角形，前两条边不产生任何差量。
	if d := tr.AddEdge("A", "B"); !d.Empty() {
		t.Fatalf("AddEdge(A,B) 应为空差量, 得到 %+v", d)
	}
	if d := tr.AddEdge("B", "C"); !d.Empty() {
		t.Fatalf("AddEdge(B,C) 应为空差量, 得到 %+v", d)
	}

	// C->A 闭合成环：A、B、C 三个单节点分量合并为一个分量。
	d := tr.AddEdge("C", "A")
	if len(d.Merges) != 1 || len(d.Splits) != 0 {
		t.Fatalf("AddEdge(C,A) 应产生一次合并, 得到 %+v", d)
	}
	m := d.Merges[0]
	if m.New.ID != "A" {
		t.Fatalf("合并后分量 ID 应为最小节点 ID \"A\", 得到 %q", m.New.ID)
	}
	if !reflect.DeepEqual(m.New.Nodes, []string{"A", "B", "C"}) {
		t.Fatalf("合并后成员应为 [A B C], 得到 %v", m.New.Nodes)
	}
	if !reflect.DeepEqual(m.OldIDs, []string{"A", "B", "C"}) {
		t.Fatalf("被合并的旧分量应为 [A B C], 得到 %v", m.OldIDs)
	}

	// 加入独立节点 D 及边 C->D：D 无法回到环，不产生合并。
	tr.AddVertex("D")
	if d := tr.AddEdge("C", "D"); !d.Empty() {
		t.Fatalf("AddEdge(C,D) 应为空差量, 得到 %+v", d)
	}
	if id, _ := tr.ComponentOf("D"); id != "D" {
		t.Fatalf("D 应自成单节点分量, 得到 %q", id)
	}

	// 删除 B->C：分量 {A,B,C} 精准分裂为三个单节点分量。
	d = tr.RemoveEdge("B", "C")
	if len(d.Splits) != 1 || len(d.Merges) != 0 {
		t.Fatalf("RemoveEdge(B,C) 应产生一次分裂, 得到 %+v", d)
	}
	s := d.Splits[0]
	if s.OldID != "A" {
		t.Fatalf("分裂的旧分量 ID 应为 \"A\", 得到 %q", s.OldID)
	}
	want := []Component{
		{ID: "A", Nodes: []string{"A"}},
		{ID: "B", Nodes: []string{"B"}},
		{ID: "C", Nodes: []string{"C"}},
	}
	if !reflect.DeepEqual(s.Parts, want) {
		t.Fatalf("分裂结果应为 %v, 得到 %v", want, s.Parts)
	}

	// D 的归属不受影响，仍为独立单节点分量。
	if id, _ := tr.ComponentOf("D"); id != "D" {
		t.Fatalf("D 的归属不应受影响, 得到 %q", id)
	}
	wantAll := append(want, Component{ID: "D", Nodes: []string{"D"}})
	if got := tr.Components(); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("最终分量应为 %v, 得到 %v", wantAll, got)
	}
}

// 分量内部的边插入不改变 SCC。
func TestAddEdge_WithinComponent(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	tr.AddEdge("B", "A")
	if d := tr.AddEdge("A", "A"); !d.Empty() {
		t.Fatalf("自环/分量内部边应为空差量, 得到 %+v", d)
	}
	if got := tr.Components(); len(got) != 1 || got[0].ID != "A" {
		t.Fatalf("分量应保持为 {A,B}, 得到 %v", got)
	}
}

// 跨分量但不成环的边插入不产生合并。
func TestAddEdge_NoCycleNoMerge(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("X", "Y")
	tr.AddEdge("Y", "X")
	if d := tr.AddEdge("Y", "Z"); !d.Empty() {
		t.Fatalf("Y->Z 不成环, 应为空差量, 得到 %+v", d)
	}
	if id, _ := tr.ComponentOf("Z"); id != "Z" {
		t.Fatalf("Z 应独立, 得到 %q", id)
	}
}

// 多分量沿路径一次性合并：{A,B} 与 {C,D} 由两条边连接成环。
func TestAddEdge_MergesMultipleComponents(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	tr.AddEdge("B", "A") // {A,B}
	tr.AddEdge("C", "D")
	tr.AddEdge("D", "C") // {C,D}
	tr.AddEdge("B", "C") // 桥接, 不成环
	d := tr.AddEdge("D", "A")
	if len(d.Merges) != 1 {
		t.Fatalf("应产生一次合并, 得到 %+v", d)
	}
	m := d.Merges[0]
	if m.New.ID != "A" || !reflect.DeepEqual(m.New.Nodes, []string{"A", "B", "C", "D"}) {
		t.Fatalf("合并结果应为 {A,B,C,D}/ID=A, 得到 %+v", m.New)
	}
	if !reflect.DeepEqual(m.OldIDs, []string{"A", "C"}) {
		t.Fatalf("被合并的旧分量应为 [A C], 得到 %v", m.OldIDs)
	}
}

// 删除不存在的边为空操作。
func TestRemoveEdge_Missing(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	if d := tr.RemoveEdge("B", "A"); !d.Empty() {
		t.Fatalf("删除不存在的边应为空差量, 得到 %+v", d)
	}
	if d := tr.RemoveEdge("X", "Y"); !d.Empty() {
		t.Fatalf("删除未知节点的边应为空差量, 得到 %+v", d)
	}
}

// 删除边后分量仍然强连通时不产生分裂。
func TestRemoveEdge_ComponentSurvives(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	tr.AddEdge("B", "C")
	tr.AddEdge("C", "A")
	tr.AddEdge("A", "C") // 冗余回边
	if d := tr.RemoveEdge("A", "C"); !d.Empty() {
		t.Fatalf("删除冗余边不应分裂, 得到 %+v", d)
	}
	if got := tr.Components(); len(got) != 1 || got[0].ID != "A" {
		t.Fatalf("分量应保持为 {A,B,C}, 得到 %v", got)
	}
}

// 删除顶点导致其所在分量分裂。
func TestRemoveVertex_SplitsComponent(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	tr.AddEdge("B", "C")
	tr.AddEdge("C", "A") // {A,B,C}
	d := tr.RemoveVertex("B")
	if len(d.Splits) != 1 {
		t.Fatalf("删除 B 应使分量分裂, 得到 %+v", d)
	}
	s := d.Splits[0]
	want := []Component{
		{ID: "A", Nodes: []string{"A"}},
		{ID: "C", Nodes: []string{"C"}},
	}
	if !reflect.DeepEqual(s.Parts, want) {
		t.Fatalf("分裂结果应为 %v, 得到 %v", want, s.Parts)
	}
	if _, ok := tr.ComponentOf("B"); ok {
		t.Fatal("B 应已被移除")
	}
}

// 删除顶点不影响其他分量。
func TestRemoveVertex_OtherComponentsUntouched(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	tr.AddEdge("B", "A") // {A,B}
	tr.AddEdge("C", "D")
	tr.AddEdge("D", "C") // {C,D}
	tr.AddEdge("B", "C")
	if d := tr.RemoveVertex("A"); !d.Empty() {
		t.Fatalf("删除 A 后 {B} 单节点, 不应有分裂差量, 得到 %+v", d)
	}
	if id, _ := tr.ComponentOf("C"); id != "C" {
		t.Fatalf("{C,D} 分量不应受影响, 得到 %q", id)
	}
	if got := tr.Components(); len(got) != 2 {
		t.Fatalf("应剩 {B} 与 {C,D} 两个分量, 得到 %v", got)
	}
}

// 分裂后再合并，分量 ID 仍稳定为最小节点 ID。
func TestSplitThenMergeAgain(t *testing.T) {
	tr := NewTracker()
	tr.AddEdge("A", "B")
	tr.AddEdge("B", "C")
	tr.AddEdge("C", "A")
	tr.RemoveEdge("B", "C")
	d := tr.AddEdge("B", "C")
	if len(d.Merges) != 1 || d.Merges[0].New.ID != "A" {
		t.Fatalf("重新合并后 ID 应恢复为 \"A\", 得到 %+v", d)
	}
}
