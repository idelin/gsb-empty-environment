package crdt

import (
	"reflect"
	"testing"
)

// 初始结构: root 下平铺 dirA、dirB。
func baseAB() map[NodeID]node {
	return NewBase(map[NodeID]BaseNode{
		RootID:  {Name: "", Parent: ""},
		"dirA":  {Name: "dirA", Parent: RootID},
		"dirB":  {Name: "dirB", Parent: RootID},
		"fileA": {Name: "a.txt", Parent: "dirA"},
		"fileB": {Name: "b.txt", Parent: "dirB"},
	})
}

// 验收边界: 节点1移动 /dirA 到 /dirB 下,节点2并发移动 /dirB 到 /dirA 下。
// 合并后必须检测到拓扑依赖环,按确定性全序撤销后发生的操作,目录不丢失。
func TestConcurrentCrossMove_CycleBrokenDeterministically(t *testing.T) {
	r1 := NewReplica("node1", baseAB())
	r2 := NewReplica("node2", baseAB())

	// 并发(各自 Lamport=1): node1: dirA -> dirB; node2: dirB -> dirA
	ts1 := r1.Move("dirA", "dirB", "")
	ts2 := r2.Move("dirB", "dirA", "")

	// 双向合并(顺序无关)
	r1.Merge(r2)
	r2.Merge(r1)

	// 1) 收敛: 两副本物化视图一致
	if !reflect.DeepEqual(r1.Snapshot(), r2.Snapshot()) {
		t.Fatalf("副本未收敛:\n r1=%v\n r2=%v", r1.Snapshot(), r2.Snapshot())
	}

	// 2) 无环不变式
	if r1.HasCycle() {
		t.Fatal("物化视图存在环")
	}

	// 3) 全序中后发生的操作被撤销。node1 < node2,故 ts2(node2 的移动)应被拒绝。
	rejected := r1.Rejected()
	if len(rejected) != 1 {
		t.Fatalf("应恰好撤销 1 条操作, 实际 %d: %+v", len(rejected), rejected)
	}
	if rejected[0].Op.ID() != ts2 {
		t.Fatalf("被撤销的应是后发生的操作 %v, 实际 %v", ts2, rejected[0].Op.ID())
	}
	if rejected[0].Op.ID() == ts1 {
		t.Fatal("先发生的操作不应被撤销")
	}

	// 4) 结果形态: dirA 位于 dirB 下, dirB 仍在根下, 无目录丢失
	if p, _ := r1.Parent("dirA"); p != "dirB" {
		t.Fatalf("dirA 的父节点应为 dirB, 实际 %v", p)
	}
	if p, _ := r1.Parent("dirB"); p != RootID {
		t.Fatalf("dirB 的父节点应为 root, 实际 %v", p)
	}
	for _, id := range []NodeID{"dirA", "dirB", "fileA", "fileB"} {
		if path, ok := r1.Path(id); !ok {
			t.Fatalf("节点 %v 丢失", id)
		} else {
			t.Logf("路径 %v -> %s", id, path)
		}
	}
	if path, _ := r1.Path("fileA"); path != "/dirB/dirA/a.txt" {
		t.Fatalf("fileA 应随 dirA 一起移动, 实际路径 %s", path)
	}
}

// 合并顺序不影响仲裁结果(交换合并方向)。
func TestMergeOrderIndependence(t *testing.T) {
	build := func(swap bool) map[NodeID][2]string {
		r1 := NewReplica("node1", baseAB())
		r2 := NewReplica("node2", baseAB())
		r1.Move("dirA", "dirB", "")
		r2.Move("dirB", "dirA", "")
		if swap {
			r1.Merge(r2)
			r2.Merge(r1)
		} else {
			r2.Merge(r1)
			r1.Merge(r2)
		}
		return r1.Snapshot()
	}
	if !reflect.DeepEqual(build(false), build(true)) {
		t.Fatal("合并顺序影响了最终结果, 仲裁不具备确定性")
	}
}

// 因果追溯: 存在因果依赖(非并发)的合法嵌套移动应全部生效。
func TestCausalChainOfMoves_AllApplied(t *testing.T) {
	r1 := NewReplica("node1", baseAB())
	r2 := NewReplica("node2", baseAB())

	r1.Move("dirA", "dirB", "") // dirA -> dirB
	r2.Merge(r1)
	r2.Move("dirB", "dirA", "") // 观察到 dirA 已在 dirB 内, 再把 dirB 移入 dirA? 成环!
	// 上面这步在因果上晚于第一步, 重放时仍应被断环拒绝。

	r1.Merge(r2)
	if len(r1.Rejected()) != 1 {
		t.Fatalf("因果链上的成环操作也应被拒绝, rejected=%v", r1.Rejected())
	}
	if r1.HasCycle() {
		t.Fatal("存在环")
	}
	if p, _ := r1.Parent("dirB"); p != RootID {
		t.Fatalf("dirB 应留在根下, 实际父节点 %v", p)
	}
}

// 深层嵌套: A->B, B->C, C->A 三方成环, 最后一条被撤销。
func TestThreeWayCycle(t *testing.T) {
	base := NewBase(map[NodeID]BaseNode{
		RootID: {Name: "", Parent: ""},
		"A":    {Name: "A", Parent: RootID},
		"B":    {Name: "B", Parent: RootID},
		"C":    {Name: "C", Parent: RootID},
	})
	r1, r2, r3 := NewReplica("n1", base), NewReplica("n2", base), NewReplica("n3", base)
	r1.Move("A", "B", "")
	r2.Move("B", "C", "")
	r3.Move("C", "A", "")

	r1.Merge(r2)
	r1.Merge(r3)
	r2.Merge(r1)
	r3.Merge(r1)

	if !reflect.DeepEqual(r1.Snapshot(), r2.Snapshot()) ||
		!reflect.DeepEqual(r2.Snapshot(), r3.Snapshot()) {
		t.Fatal("三副本未收敛")
	}
	if r1.HasCycle() {
		t.Fatal("存在环")
	}
	if len(r1.Rejected()) != 1 {
		t.Fatalf("应撤销 1 条成环操作, 实际 %d", len(r1.Rejected()))
	}
	// n1<n2<n3, 全序中最后的是 n3 的 C->A, 应被撤销
	if got := r1.Rejected()[0].Op; got.Node != "C" || got.NewParent != "A" {
		t.Fatalf("被撤销的操作应为 C->A, 实际 %v->%v", got.Node, got.NewParent)
	}
}

// 重命名 + 移动组合, 以及并发同名冲突的确定性消解。
func TestRenameAndNameConflict(t *testing.T) {
	base := NewBase(map[NodeID]BaseNode{
		RootID: {Name: "", Parent: ""},
		"dirA": {Name: "dirA", Parent: RootID},
		"dirB": {Name: "dirB", Parent: RootID},
		"x":    {Name: "doc", Parent: "dirA"},
		"y":    {Name: "doc", Parent: "dirB"},
	})
	r1 := NewReplica("node1", base)
	r2 := NewReplica("node2", base)

	// 并发(均 Lamport=1): 全序 node1 < node2。
	// 重放时 x 先移入 dirB, 与仍在 dirB 的 y(同名 doc)冲突 -> 确定性加后缀。
	r1.Move("x", "dirB", "")
	r2.Move("y", RootID, "readme.md")

	r1.Merge(r2)
	r2.Merge(r1)

	if !reflect.DeepEqual(r1.Snapshot(), r2.Snapshot()) {
		t.Fatal("副本未收敛")
	}
	if r1.HasCycle() {
		t.Fatal("存在环")
	}
	// x 与 y 在 dirB 下同名 doc, x 确定性改名为 doc@node1
	if path, _ := r1.Path("x"); path != "/dirB/doc@node1" {
		t.Fatalf("x 路径错误: %s", path)
	}
	if path, _ := r1.Path("y"); path != "/readme.md" {
		t.Fatalf("y 路径错误: %s", path)
	}
}

// 幂等性: 重复合并不改变结果。
func TestMergeIdempotent(t *testing.T) {
	r1 := NewReplica("node1", baseAB())
	r2 := NewReplica("node2", baseAB())
	r1.Move("dirA", "dirB", "")
	r2.Move("dirB", "dirA", "")
	r1.Merge(r2)
	before := r1.Snapshot()
	r1.Merge(r2)
	r1.Merge(r2)
	if !reflect.DeepEqual(before, r1.Snapshot()) {
		t.Fatal("合并不幂等")
	}
	if len(r1.Log()) != 2 {
		t.Fatalf("日志应按操作 ID 去重, 实际长度 %d", len(r1.Log()))
	}
}

// 非法操作防护: 根不可移动、不能移入自身。
func TestInvalidMovesRejected(t *testing.T) {
	r1 := NewReplica("node1", baseAB())
	r1.Move(RootID, "dirA", "")
	r1.Move("dirA", "dirA", "")
	if len(r1.Rejected()) != 2 {
		t.Fatalf("两条非法操作都应被拒绝, 实际 %d", len(r1.Rejected()))
	}
	if p, _ := r1.Parent(RootID); p != "" {
		t.Fatal("根节点被移动")
	}
}
