package hieragg

import (
	"reflect"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustSum(t *testing.T, g *Graph, id string, want float64) {
	t.Helper()
	got, err := g.Sum(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Sum(%q) = %v, want %v", id, got, want)
	}
}

func mustLeaves(t *testing.T, g *Graph, id string, want []string) {
	t.Helper()
	got, err := g.Leaves(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Leaves(%q) = %v, want %v", id, got, want)
	}
}

// 菱形共享：root -> {a, b}，a 和 b 都含叶 l1。
// 若错误地把子节点汇总直接相加，root 会把 l1 计两次。
func TestSharedLeafCountedOncePerParent(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 10))
	must(t, g.AddNode("a"))
	must(t, g.AddNode("b"))
	must(t, g.AddNode("root"))

	mustAdd := func(p, c string) Update {
		u, err := g.AddEdge(p, c)
		must(t, err)
		return u
	}
	u := mustAdd("a", "l1")
	if u.Deltas["a"] != 10 || !reflect.DeepEqual(u.Added["a"], []string{"l1"}) {
		t.Errorf("unexpected update for a<-l1: %+v", u)
	}
	mustAdd("b", "l1")
	mustAdd("root", "a")

	// root 已含 l1（经 a），再接 b 时 l1 不得重复计入。
	u = mustAdd("root", "b")
	if u.Deltas["root"] != 0 {
		t.Errorf("shared leaf double counted: delta=%v, update=%+v", u.Deltas["root"], u)
	}

	mustSum(t, g, "a", 10)
	mustSum(t, g, "b", 10) // 不同上级分别计算
	mustSum(t, g, "root", 10)
	mustLeaves(t, g, "root", []string{"l1"})
}

func TestSetLeafValuePropagatesToAllAncestors(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 10))
	must(t, g.AddNode("a"))
	must(t, g.AddNode("b"))
	must(t, g.AddNode("root"))
	for _, e := range [][2]string{{"a", "l1"}, {"b", "l1"}, {"root", "a"}, {"root", "b"}} {
		_, err := g.AddEdge(e[0], e[1])
		must(t, err)
	}

	u, err := g.SetLeafValue("l1", 25)
	must(t, err)
	want := map[string]float64{"l1": 15, "a": 15, "b": 15, "root": 15}
	if !reflect.DeepEqual(u.Deltas, want) {
		t.Errorf("Deltas = %v, want %v", u.Deltas, want)
	}
	mustSum(t, g, "root", 25)
	mustSum(t, g, "a", 25)
	mustSum(t, g, "b", 25)
}

// 多层结构 + 部分共享：验证 AddEdge 只传播差集。
func TestAddEdgePropagatesOnlyNewLeaves(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 1))
	must(t, g.AddLeaf("l2", 2))
	must(t, g.AddLeaf("l3", 4))
	must(t, g.AddNode("a"))
	must(t, g.AddNode("b"))
	must(t, g.AddNode("root"))
	for _, e := range [][2]string{
		{"a", "l1"}, {"a", "l2"},
		{"b", "l2"}, {"b", "l3"},
		{"root", "a"},
	} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	u, err := g.AddEdge("root", "b")
	must(t, err)
	// root 已有 {l1,l2}，从 b 只新增 l3。
	if u.Deltas["root"] != 4 || !reflect.DeepEqual(u.Added["root"], []string{"l3"}) {
		t.Errorf("unexpected update: %+v", u)
	}
	mustSum(t, g, "root", 7)
	mustLeaves(t, g, "root", []string{"l1", "l2", "l3"})
}

// 删除一条路径后，共享叶仍经其他路径可达，汇总不变；删除最后一条路径才消失。
func TestRemoveEdgeKeepsLeafReachableViaOtherPath(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 10))
	must(t, g.AddLeaf("l2", 5))
	must(t, g.AddNode("a"))
	must(t, g.AddNode("b"))
	must(t, g.AddNode("root"))
	for _, e := range [][2]string{
		{"a", "l1"}, {"b", "l1"}, {"b", "l2"},
		{"root", "a"}, {"root", "b"},
	} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	mustSum(t, g, "root", 15)

	// 删除 a->l1：a 失去 l1，但 root 经 b 仍可达 l1。
	u, err := g.RemoveEdge("a", "l1")
	must(t, err)
	if u.Deltas["a"] != -10 || !reflect.DeepEqual(u.Removed["a"], []string{"l1"}) {
		t.Errorf("unexpected update for a: %+v", u)
	}
	if _, ok := u.Deltas["root"]; ok {
		t.Errorf("root should be unaffected, got %+v", u)
	}
	mustSum(t, g, "a", 0)
	mustSum(t, g, "root", 15)
	mustLeaves(t, g, "root", []string{"l1", "l2"})

	// 再删除 b->l1：root 与 b 同时失去 l1。
	u, err = g.RemoveEdge("b", "l1")
	must(t, err)
	if u.Deltas["b"] != -10 || u.Deltas["root"] != -10 {
		t.Errorf("unexpected deltas: %+v", u.Deltas)
	}
	mustSum(t, g, "b", 5)
	mustSum(t, g, "root", 5)
	mustLeaves(t, g, "root", []string{"l2"})
}

// 删除中间边会级联重算更上层祖先。
func TestRemoveEdgeCascadesRecompute(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 3))
	must(t, g.AddNode("mid"))
	must(t, g.AddNode("top"))
	must(t, g.AddNode("root"))
	for _, e := range [][2]string{
		{"mid", "l1"}, {"top", "mid"}, {"root", "top"},
	} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	mustSum(t, g, "root", 3)

	u, err := g.RemoveEdge("top", "mid")
	must(t, err)
	want := map[string]float64{"top": -3, "root": -3}
	if !reflect.DeepEqual(u.Deltas, want) {
		t.Errorf("Deltas = %v, want %v", u.Deltas, want)
	}
	mustSum(t, g, "mid", 3) // mid 自身子树不受影响
	mustSum(t, g, "top", 0)
	mustSum(t, g, "root", 0)
}

func TestCycleRejected(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 1))
	must(t, g.AddNode("a"))
	must(t, g.AddNode("b"))
	if _, err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("b", "l1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("b", "a"); err == nil {
		t.Error("expected cycle error for b->a")
	}
	if _, err := g.AddEdge("l1", "a"); err == nil {
		t.Error("expected error: leaf cannot have children")
	}
}

func TestIdempotentAndErrors(t *testing.T) {
	g := New()
	must(t, g.AddLeaf("l1", 1))
	must(t, g.AddNode("a"))
	if _, err := g.AddEdge("a", "l1"); err != nil {
		t.Fatal(err)
	}
	u, err := g.AddEdge("a", "l1") // 重复加边：幂等
	must(t, err)
	if len(u.Deltas) != 0 {
		t.Errorf("duplicate edge should be a no-op, got %+v", u)
	}
	mustSum(t, g, "a", 1)

	if err := g.AddLeaf("l1", 2); err == nil {
		t.Error("expected duplicate node error")
	}
	if _, err := g.RemoveEdge("a", "l1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RemoveEdge("a", "l1"); err == nil {
		t.Error("expected missing edge error")
	}
	if _, err := g.SetLeafValue("ghost", 1); err == nil {
		t.Error("expected unknown leaf error")
	}
}

// 混合场景：改值 + 加边 + 删边连续作用，最终与全量重算一致。
func TestIncrementalMatchesFullRecompute(t *testing.T) {
	g := New()
	leaves := map[string]float64{"l1": 1, "l2": 2, "l3": 3, "l4": 4}
	for id, v := range leaves {
		must(t, g.AddLeaf(id, v))
	}
	for _, n := range []string{"x", "y", "z", "root"} {
		must(t, g.AddNode(n))
	}
	edges := [][2]string{
		{"x", "l1"}, {"x", "l2"},
		{"y", "l2"}, {"y", "l3"},
		{"z", "l3"}, {"z", "l4"},
		{"root", "x"}, {"root", "y"},
	}
	for _, e := range edges {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.SetLeafValue("l2", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("root", "z"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RemoveEdge("y", "l2"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SetLeafValue("l3", 30); err != nil {
		t.Fatal(err)
	}

	// 全量重算基准。
	want := map[string]float64{}
	wantLeaves := map[string]map[string]bool{}
	var recompute func(n string) map[string]bool
	recompute = func(n string) map[string]bool {
		if s, ok := wantLeaves[n]; ok {
			return s
		}
		s := map[string]bool{}
		if g.isLeaf[n] {
			s[n] = true
		} else {
			for ch := range g.children[n] {
				for l := range recompute(ch) {
					s[l] = true
				}
			}
		}
		wantLeaves[n] = s
		for l := range s {
			want[n] += g.leafVal[l]
		}
		return s
	}
	for n := range g.isLeaf {
		recompute(n)
	}
	for n, w := range want {
		mustSum(t, g, n, w)
	}
}
