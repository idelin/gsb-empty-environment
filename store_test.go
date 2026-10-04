package main

import (
	"testing"
	"time"
)

func hasBranch(vals []Value, data string, deleted bool, writer string) bool {
	for _, v := range vals {
		if v.Data == data && v.Deleted == deleted && v.Writer == writer {
			return true
		}
	}
	return false
}

// 并发版本必须保留：读返回的兄弟集合与修复后的副本都不能丢分支
func TestConcurrentBranchesPreserved(t *testing.T) {
	n0 := NewNode("n0", time.Millisecond)
	n1 := NewNode("n1", time.Millisecond)
	n2 := NewNode("n2", time.Millisecond)
	c := NewCluster(2, n0, n1, n2)

	n0.writeLocal("k", "vA", false, "A")
	n1.writeLocal("k", "vB", false, "B")
	n2.writeLocal("k", "vA", false, "A") // n2 只同步到 A 的分支

	res := c.Read("k")
	if len(res.Siblings) != 2 {
		t.Fatalf("期望2个并发分支, 实际 %d: %v", len(res.Siblings), res.Siblings)
	}
	if !hasBranch(res.Siblings, "vA", false, "A") || !hasBranch(res.Siblings, "vB", false, "B") {
		t.Fatalf("返回值丢失并发分支: %v", res.Siblings)
	}

	c.WaitRepair()
	for i, n := range c.nodes {
		got := c.Versions(i, "k")
		if len(got) != 2 ||
			!hasBranch(got, "vA", false, "A") ||
			!hasBranch(got, "vB", false, "B") {
			t.Fatalf("修复后 %s 丢失并发分支: %v", n.id, got)
		}
	}
}

// 迟到响应携带的分支必须并入修复内容，最终所有副本收敛到全部分支
func TestLateResponseMergedIntoRepair(t *testing.T) {
	fast0 := NewNode("fast0", time.Millisecond)
	fast1 := NewNode("fast1", time.Millisecond)
	slow := NewNode("slow", 50*time.Millisecond) // 迟到节点
	c := NewCluster(2, fast0, fast1, slow)

	fast0.writeLocal("k", "vA", false, "A")
	fast1.writeLocal("k", "vA", false, "A")
	slow.writeLocal("k", "vB", false, "B") // 并发分支只在慢节点上

	res := c.Read("k") // 法定人数=2，返回时慢节点响应尚未到达
	if len(res.Siblings) != 1 || res.Winner.Data != "vA" {
		t.Fatalf("法定人数内应只看到 vA: %v", res.Siblings)
	}

	c.WaitRepair() // 迟到响应并入修复
	for i, n := range c.nodes {
		got := c.Versions(i, "k")
		if len(got) != 2 ||
			!hasBranch(got, "vA", false, "A") ||
			!hasBranch(got, "vB", false, "B") {
			t.Fatalf("迟到分支丢失 @%s: %v", n.id, got)
		}
	}
}

// 墓碑支配旧值：旧值不能复活，修复不得用旧值覆盖墓碑
func TestTombstoneNotOverwritten(t *testing.T) {
	n0 := NewNode("n0", time.Millisecond)
	n1 := NewNode("n1", time.Millisecond)
	c := NewCluster(2, n0, n1)

	n0.writeLocal("k", "v1", false, "A")
	n1.writeLocal("k", "v1", false, "A")
	n0.writeLocal("k", "", true, "A") // 删除 -> 墓碑 {A:2}

	res := c.Read("k")
	if !res.Winner.Deleted {
		t.Fatalf("胜者应为墓碑: %v", res.Winner)
	}
	if len(res.Siblings) != 1 {
		t.Fatalf("旧值应被墓碑支配而丢弃: %v", res.Siblings)
	}

	c.WaitRepair()
	got := c.Versions(1, "k")
	if len(got) != 1 || !got[0].Deleted {
		t.Fatalf("n1 的墓碑被旧值覆盖或旧值复活: %v", got)
	}

	// 直接对墓碑节点发起携带旧值的修复：必须被拒绝（条件修复）
	stale := []Value{{Clock: VClock{"A": 1}, Data: "v1", Writer: "A"}}
	if n0.repair("k", stale) {
		t.Fatalf("旧值修复不应改变墓碑节点")
	}
	got = c.Versions(0, "k")
	if len(got) != 1 || !got[0].Deleted {
		t.Fatalf("墓碑被旧值修复覆盖: %v", got)
	}
}

// 修复在途时目标被并发更新：条件修复必须归并而非覆盖
func TestConditionalRepairMergesConcurrentUpdate(t *testing.T) {
	n0 := NewNode("n0", time.Millisecond)
	n1 := NewNode("n1", time.Millisecond)

	// n0 上：A 连续两写，读到的新版本为 {A:2}
	n0.writeLocal("k", "old", false, "A")
	n0.writeLocal("k", "updated", false, "A")
	payload := n0.get("k") // 修复内容 {A:2}

	// n1 停留在 {A:1}，修复到达前被 B 并发更新为 {A:1,B:1}
	n1.writeLocal("k", "old", false, "A")
	n1.writeLocal("k", "concurrent-new", false, "B")

	if !n1.repair("k", payload) {
		t.Fatalf("并发更新后修复应触发归并")
	}
	got := n1.get("k")
	if len(got) != 2 ||
		!hasBranch(got, "updated", false, "A") ||
		!hasBranch(got, "concurrent-new", false, "B") {
		t.Fatalf("条件修复丢失并发分支: %v", got)
	}

	// 修复内容已被目标支配时：修复应为无操作
	stale := []Value{{Clock: VClock{"A": 1}, Data: "old", Writer: "A"}}
	if n1.repair("k", stale) {
		t.Fatalf("目标已领先时修复不应产生变更")
	}
	if got := n1.get("k"); len(got) != 2 {
		t.Fatalf("无操作修复改变了目标状态: %v", got)
	}
}

// 向量时钟比较
func TestVClockCompare(t *testing.T) {
	a := VClock{"x": 1}
	b := VClock{"x": 2}
	cc := VClock{"y": 1}
	if a.Compare(b) != Less || b.Compare(a) != Greater {
		t.Fatal("支配关系错误")
	}
	if a.Compare(cc) != Concurrent {
		t.Fatal("并发关系错误")
	}
	if a.Compare(a.Copy()) != Equal {
		t.Fatal("相等关系错误")
	}
}
