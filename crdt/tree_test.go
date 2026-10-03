package crdt

import (
	"testing"
)

// bootstrap 构造两个初始状态一致的副本：/dirA 与 /dirB。
func bootstrap(t *testing.T) (n1, n2 *Tree) {
	t.Helper()
	n1, n2 = NewTree("node1"), NewTree("node2")
	for _, tr := range []*Tree{n1, n2} {
		if err := tr.Mkdir(RootID, "dirA", "dirA"); err != nil {
			t.Fatal(err)
		}
		if err := tr.Mkdir(RootID, "dirB", "dirB"); err != nil {
			t.Fatal(err)
		}
	}
	return n1, n2
}

func mustPath(t *testing.T, tr *Tree, id string) string {
	t.Helper()
	p, err := tr.Path(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// 验收场景：节点1移动 /dirA 到 /dirB 下，节点2并发移动 /dirB 到 /dirA 下。
// 合并后必须检测到拓扑依赖环，依据确定性全序撤销后发生的操作，且无目录丢失。
func TestConcurrentCycleMove(t *testing.T) {
	n1, n2 := bootstrap(t)

	op1, err := n1.Move("dirA", "dirB") // 1@node1: /dirA -> /dirB/dirA
	if err != nil {
		t.Fatal(err)
	}
	op2, err := n2.Move("dirB", "dirA") // 1@node2: /dirB -> /dirA/dirB
	if err != nil {
		t.Fatal(err)
	}

	// 并发交换日志（双向合并）。
	n1.Merge(n2.Ops())
	n2.Merge(n1.Ops())

	for i, tr := range []*Tree{n1, n2} {
		if !tr.IsAcyclic() {
			t.Fatalf("replica %d: tree contains a cycle", i)
		}
		if !tr.Contains("dirA") || !tr.Contains("dirB") {
			t.Fatalf("replica %d: directory lost after merge", i)
		}
		undone := tr.Undone()
		if len(undone) != 1 {
			t.Fatalf("replica %d: expect exactly 1 undone op, got %d", i, len(undone))
		}
		// 确定性全序 1@node1 < 1@node2，后发生的 op2 必须被撤销。
		if undone[0].ID != op2.ID {
			t.Fatalf("replica %d: later op %v should be undone, got %v", i, op2.ID, undone[0].ID)
		}
		if undone[0].ID == op1.ID {
			t.Fatalf("replica %d: earlier op %v must win arbitration", i, op1.ID)
		}
	}

	// 两个副本收敛到相同拓扑：/dirB/dirA。
	if got := mustPath(t, n1, "dirA"); got != "/dirB/dirA" {
		t.Fatalf("node1: dirA path = %s, want /dirB/dirA", got)
	}
	if got := mustPath(t, n2, "dirA"); got != "/dirB/dirA" {
		t.Fatalf("node2: dirA path = %s, want /dirB/dirA", got)
	}
	if got := mustPath(t, n1, "dirB"); got != "/dirB" {
		t.Fatalf("node1: dirB path = %s, want /dirB", got)
	}
}

// 合并顺序无关性：无论日志以何种顺序到达，所有副本收敛到同一状态。
func TestMergeOrderIndependence(t *testing.T) {
	n1, n2 := bootstrap(t)
	op1, _ := n1.Move("dirA", "dirB")
	op2, _ := n2.Move("dirB", "dirA")

	a, b := NewTree("a"), NewTree("b")
	for _, tr := range []*Tree{a, b} {
		tr.Mkdir(RootID, "dirA", "dirA")
		tr.Mkdir(RootID, "dirB", "dirB")
	}
	a.Merge([]MoveOp{op1, op2})
	b.Merge([]MoveOp{op2, op1}) // 逆序到达
	b.Merge([]MoveOp{op1})      // 重复到达（幂等）

	if mustPath(t, a, "dirA") != mustPath(t, b, "dirA") ||
		mustPath(t, a, "dirB") != mustPath(t, b, "dirB") {
		t.Fatal("replicas diverged under different merge orders")
	}
	if !a.IsAcyclic() || !b.IsAcyclic() {
		t.Fatal("cycle detected")
	}
}

// 无冲突的顺序移动全部生效。
func TestSequentialMoves(t *testing.T) {
	tr := NewTree("node1")
	tr.Mkdir(RootID, "a", "a")
	tr.Mkdir(RootID, "b", "b")
	tr.Mkdir(RootID, "c", "c")

	if _, err := tr.Move("a", "b"); err != nil { // /b/a
		t.Fatal(err)
	}
	if _, err := tr.Move("b", "c"); err != nil { // /c/b/a
		t.Fatal(err)
	}
	if got := mustPath(t, tr, "a"); got != "/c/b/a" {
		t.Fatalf("path = %s, want /c/b/a", got)
	}
	if len(tr.Undone()) != 0 {
		t.Fatalf("no op should be undone, got %v", tr.Undone())
	}
	if !tr.IsAcyclic() {
		t.Fatal("cycle detected")
	}
}

// 深层嵌套的因果追溯：移动祖先到其孙节点下必须被撤销。
func TestDeepCycleDetection(t *testing.T) {
	tr := NewTree("node1")
	tr.Mkdir(RootID, "a", "a")
	tr.Mkdir("a", "b", "b")
	tr.Mkdir("b", "c", "c")

	if _, err := tr.Move("a", "c"); err != nil { // /a -> /a/b/c/a 成环
		t.Fatal(err)
	}
	if len(tr.Undone()) != 1 {
		t.Fatalf("cycle-creating op must be undone, undone=%v", tr.Undone())
	}
	if got := mustPath(t, tr, "a"); got != "/a" {
		t.Fatalf("path = %s, want /a", got)
	}
	if !tr.IsAcyclic() {
		t.Fatal("cycle detected")
	}
}

// 移动自身到自身下也必须被撤销。
func TestSelfMoveRejected(t *testing.T) {
	tr := NewTree("node1")
	tr.Mkdir(RootID, "a", "a")
	if _, err := tr.Move("a", "a"); err != nil {
		t.Fatal(err)
	}
	if len(tr.Undone()) != 1 || !tr.IsAcyclic() {
		t.Fatal("self move must be undone")
	}
}

// 重命名通过移动操作携带新名字完成。
func TestRename(t *testing.T) {
	tr := NewTree("node1")
	tr.Mkdir(RootID, "a", "a")
	if _, err := tr.MoveRename("a", RootID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if got := mustPath(t, tr, "a"); got != "/renamed" {
		t.Fatalf("path = %s, want /renamed", got)
	}
	id, err := tr.Lookup("/renamed")
	if err != nil || id != "a" {
		t.Fatalf("lookup /renamed failed: %v", err)
	}
}

// 同目录重名冲突：全序中较晚的操作被撤销。
func TestNameClashArbitration(t *testing.T) {
	n1, n2 := NewTree("node1"), NewTree("node2")
	for _, tr := range []*Tree{n1, n2} {
		tr.Mkdir(RootID, "a", "a")
		tr.Mkdir(RootID, "b", "b")
	}
	n1.MoveRename("a", RootID, "same") // 1@node1
	n2.MoveRename("b", RootID, "same") // 1@node2，并发重名
	n1.Merge(n2.Ops())
	n2.Merge(n1.Ops())

	if len(n1.Undone()) != 1 || n1.Undone()[0].ID.Replica != "node2" {
		t.Fatalf("later rename must be undone, undone=%v", n1.Undone())
	}
	if mustPath(t, n1, "a") != "/same" || mustPath(t, n1, "b") != "/b" {
		t.Fatal("name clash arbitration wrong")
	}
	if mustPath(t, n1, "a") != mustPath(t, n2, "a") {
		t.Fatal("replicas diverged")
	}
}

// Lamport 时钟在合并时因果推进。
func TestLamportClockAdvance(t *testing.T) {
	n1, n2 := bootstrap(t)
	n1.Move("dirA", "dirB") // clock=1
	n1.Move("dirA", RootID) // clock=2
	n2.Merge(n1.Ops())
	op, err := n2.Move("dirB", "dirA")
	if err != nil {
		t.Fatal(err)
	}
	if op.ID.Lamport != 3 {
		t.Fatalf("lamport = %d, want 3 (max(0,2)+1)", op.ID.Lamport)
	}
}

// 移动根节点必须被拒绝。
func TestMoveRootRejected(t *testing.T) {
	tr := NewTree("node1")
	tr.Mkdir(RootID, "a", "a")
	if _, err := tr.Move(RootID, "a"); err != ErrMoveRoot {
		t.Fatalf("want ErrMoveRoot, got %v", err)
	}
}
