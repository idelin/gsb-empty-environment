package dagagg

import (
	"errors"
	"reflect"
	"testing"
)

func mustAgg(t *testing.T, g *Graph, id NodeID, want float64) {
	t.Helper()
	got, err := g.Aggregate(id)
	if err != nil {
		t.Fatalf("Aggregate(%s): %v", id, err)
	}
	if got != want {
		t.Fatalf("Aggregate(%s) = %v, want %v", id, got, want)
	}
}

func mustLeaves(t *testing.T, g *Graph, id NodeID, want ...NodeID) {
	t.Helper()
	got, err := g.SourceLeaves(id)
	if err != nil {
		t.Fatalf("SourceLeaves(%s): %v", id, err)
	}
	if want == nil {
		want = []NodeID{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SourceLeaves(%s) = %v, want %v", id, got, want)
	}
}

func deltaFor(deltas []Delta, id NodeID) *Delta {
	for i := range deltas {
		if deltas[i].Node == id {
			return &deltas[i]
		}
	}
	return nil
}

// 菱形共享：root -> {A, B}，A、B 共享叶 L。
// root 对 L 只计一次，A、B 各自计入 L。
func TestSharedLeafCountedOncePerParent(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"root", "A", "B"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L", 10); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLeaf("L2", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("root", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("root", "B"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("A", "L"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("A", "L2"); err != nil {
		t.Fatal(err)
	}
	// B 共享 L：root 的汇总只应增加一次 L。
	deltas, err := g.AddEdge("B", "L")
	if err != nil {
		t.Fatal(err)
	}
	if d := deltaFor(deltas, "B"); d == nil || d.Value != 10 {
		t.Fatalf("delta for B = %+v, want value 10", d)
	}
	if d := deltaFor(deltas, "root"); d != nil {
		t.Fatalf("root should be unaffected, got %+v", d)
	}

	mustAgg(t, g, "A", 15)
	mustAgg(t, g, "B", 10)
	mustAgg(t, g, "root", 15) // 不是 15+10：共享叶 L 只计一次
	mustLeaves(t, g, "root", "L", "L2")
	mustLeaves(t, g, "B", "L")
}

// 同一上级经多条路径到达同一叶，仍只计一次。
func TestMultiPathDedup(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"root", "A", "M"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("root", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("A", "M"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("M", "L"); err != nil {
		t.Fatal(err)
	}
	// root -> M 形成第二条到达 L 的路径。
	deltas, err := g.AddEdge("root", "M")
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 0 {
		t.Fatalf("expected no deltas, got %+v", deltas)
	}
	mustAgg(t, g, "root", 7)
	mustLeaves(t, g, "root", "L")
}

// 删边：共享叶在另一侧仍可达时上级不变，仅被删侧减少。
func TestRemoveEdgeKeepsSharedLeaf(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"root", "A", "B"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L", 10); err != nil {
		t.Fatal(err)
	}
	for _, e := range [][2]NodeID{{"root", "A"}, {"root", "B"}, {"A", "L"}, {"B", "L"}} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}

	deltas, err := g.RemoveEdge("A", "L")
	if err != nil {
		t.Fatal(err)
	}
	if d := deltaFor(deltas, "A"); d == nil || d.Value != -10 || !reflect.DeepEqual(d.Removed, []NodeID{"L"}) {
		t.Fatalf("delta for A = %+v, want value -10 removed [L]", d)
	}
	if d := deltaFor(deltas, "root"); d != nil {
		t.Fatalf("root should keep L via B, got %+v", d)
	}
	mustAgg(t, g, "A", 0)
	mustAgg(t, g, "B", 10)
	mustAgg(t, g, "root", 10)
	mustLeaves(t, g, "root", "L")

	// 再删 B->L，root 才失去 L。
	deltas, err = g.RemoveEdge("B", "L")
	if err != nil {
		t.Fatal(err)
	}
	if d := deltaFor(deltas, "root"); d == nil || d.Value != -10 {
		t.Fatalf("delta for root = %+v, want value -10", d)
	}
	mustAgg(t, g, "root", 0)
	mustLeaves(t, g, "root")
}

// 删边沿多层祖先传播，且只影响真正失去叶的祖先。
func TestRemoveEdgePropagatesUpward(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"top", "mid", "low", "alt"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L", 3); err != nil {
		t.Fatal(err)
	}
	// top -> mid -> low -> L，且 top -> alt -> low（low 被两条路径共享）。
	for _, e := range [][2]NodeID{{"top", "mid"}, {"mid", "low"}, {"top", "alt"}, {"alt", "low"}, {"low", "L"}} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	mustAgg(t, g, "top", 3)

	deltas, err := g.RemoveEdge("mid", "low")
	if err != nil {
		t.Fatal(err)
	}
	if d := deltaFor(deltas, "mid"); d == nil || d.Value != -3 {
		t.Fatalf("delta for mid = %+v, want -3", d)
	}
	// top 仍经 alt -> low 到达 L，不应变化。
	if d := deltaFor(deltas, "top"); d != nil {
		t.Fatalf("top should be unaffected, got %+v", d)
	}
	mustAgg(t, g, "top", 3)
	mustAgg(t, g, "mid", 0)
}

// 加边增量：只传播新增叶，差量携带来源叶集合。
func TestAddEdgeDelta(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"root", "A"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L1", 1); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLeaf("L2", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("root", "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("A", "L1"); err != nil {
		t.Fatal(err)
	}

	deltas, err := g.AddEdge("A", "L2")
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %+v", deltas)
	}
	d := deltaFor(deltas, "A")
	if d == nil || d.Value != 2 || !reflect.DeepEqual(d.Added, []NodeID{"L2"}) {
		t.Fatalf("delta for A = %+v, want value 2 added [L2]", d)
	}
	d = deltaFor(deltas, "root")
	if d == nil || d.Value != 2 || !reflect.DeepEqual(d.Added, []NodeID{"L2"}) {
		t.Fatalf("delta for root = %+v, want value 2 added [L2]", d)
	}
	mustAgg(t, g, "root", 3)
	mustLeaves(t, g, "root", "L1", "L2")
}

// 叶值修改传播到所有祖先，差量一致且带来源叶。
func TestSetLeafValue(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"root", "A", "B"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L", 10); err != nil {
		t.Fatal(err)
	}
	for _, e := range [][2]NodeID{{"root", "A"}, {"root", "B"}, {"A", "L"}, {"B", "L"}} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}

	deltas, err := g.SetLeafValue("L", 14)
	if err != nil {
		t.Fatal(err)
	}
	// L 自身 + A + B + root 各 +4。
	if len(deltas) != 4 {
		t.Fatalf("expected 4 deltas, got %+v", deltas)
	}
	for _, id := range []NodeID{"L", "A", "B", "root"} {
		d := deltaFor(deltas, id)
		if d == nil || d.Value != 4 || !reflect.DeepEqual(d.Changed, []NodeID{"L"}) {
			t.Fatalf("delta for %s = %+v, want value 4 changed [L]", id, d)
		}
	}
	mustAgg(t, g, "root", 14)
	mustAgg(t, g, "A", 14)
	mustAgg(t, g, "B", 14)
}

func TestCycleRejected(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"a", "b", "c"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.AddEdge("a", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("b", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("c", "a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("want ErrCycle, got %v", err)
	}
	if _, err := g.AddEdge("a", "a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("want ErrCycle for self-loop, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	g := NewGraph()
	if err := g.AddNode("n"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddNode("n"); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("want ErrNodeExists, got %v", err)
	}
	if err := g.AddLeaf("l", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("n", "missing"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
	if _, err := g.AddEdge("n", "l"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdge("n", "l"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("want ErrEdgeExists, got %v", err)
	}
	if _, err := g.RemoveEdge("l", "n"); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("want ErrEdgeNotFound, got %v", err)
	}
	if _, err := g.SetLeafValue("n", 5); !errors.Is(err, ErrNotLeaf) {
		t.Fatalf("want ErrNotLeaf, got %v", err)
	}
	if _, err := g.Aggregate("missing"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
}

// 内部子树整体挂接：加边后新子树的全部叶按去重并入祖先。
func TestAttachSubtree(t *testing.T) {
	g := NewGraph()
	for _, id := range []NodeID{"root", "sub", "x", "y"} {
		if err := g.AddNode(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddLeaf("L1", 1); err != nil {
		t.Fatal(err)
	}
	if err := g.AddLeaf("L2", 2); err != nil {
		t.Fatal(err)
	}
	// 子树 sub -> {x, y}，x、y 共享 L1，y 另有 L2。
	for _, e := range [][2]NodeID{{"sub", "x"}, {"sub", "y"}, {"x", "L1"}, {"y", "L1"}, {"y", "L2"}} {
		if _, err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	mustAgg(t, g, "sub", 3)

	deltas, err := g.AddEdge("root", "sub")
	if err != nil {
		t.Fatal(err)
	}
	d := deltaFor(deltas, "root")
	if d == nil || d.Value != 3 || !reflect.DeepEqual(d.Added, []NodeID{"L1", "L2"}) {
		t.Fatalf("delta for root = %+v, want value 3 added [L1 L2]", d)
	}
	mustAgg(t, g, "root", 3)
	mustLeaves(t, g, "root", "L1", "L2")
}
