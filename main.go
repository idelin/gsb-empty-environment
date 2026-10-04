package main

import (
	"fmt"
	"time"
)

func main() {
	fmt.Println("=== 场景1：并发写 + 迟到响应，读修复保留全部分支 ===")
	// n2 延迟最高，其响应在读返回后才到达（迟到响应）
	n0 := NewNode("n0", 1*time.Millisecond)
	n1 := NewNode("n1", 2*time.Millisecond)
	n2 := NewNode("n2", 30*time.Millisecond)
	c := NewCluster(2, n0, n1, n2)

	// 两个并发写分别落在 n0、n1；n2 持有更旧的版本
	n0.writeLocal("k", "v-from-A", false, "A")
	n1.writeLocal("k", "v-from-B", false, "B")
	n2.writeLocal("k", "v-stale", false, "C")

	res := c.Read("k")
	fmt.Printf("读返回胜者: %v\n", res.Winner)
	fmt.Printf("读返回兄弟分支(%d个): %v\n", len(res.Siblings), res.Siblings)
	c.WaitRepair()
	for i, n := range c.nodes {
		fmt.Printf("修复后 %s: %v\n", n.id, c.Versions(i, "k"))
	}

	fmt.Println("\n=== 场景2：墓碑不被旧值覆盖 ===")
	m0 := NewNode("m0", 1*time.Millisecond)
	m1 := NewNode("m1", 2*time.Millisecond)
	m2 := NewNode("m2", 3*time.Millisecond)
	c2 := NewCluster(2, m0, m1, m2)

	// m0 先写后删（墓碑版本更新），m1/m2 只同步到删除前的旧值
	m0.writeLocal("k", "v1", false, "A")
	m1.writeLocal("k", "v1", false, "A")
	m2.writeLocal("k", "v1", false, "A")
	m0.writeLocal("k", "", true, "A") // 删除，产生墓碑

	res2 := c2.Read("k")
	fmt.Printf("读返回胜者: %v (Deleted=%v)\n", res2.Winner, res2.Winner.Deleted)
	c2.WaitRepair()
	for i, n := range c2.nodes {
		fmt.Printf("修复后 %s: %v\n", n.id, c2.Versions(i, "k"))
	}

	fmt.Println("\n=== 场景3：修复在途时目标已被并发更新 ===")
	p0 := NewNode("p0", 1*time.Millisecond)
	p1 := NewNode("p1", 2*time.Millisecond)

	// p0 上 A 连续两写，读出的修复内容为 {A:2}
	p0.writeLocal("k", "old", false, "A")
	p0.writeLocal("k", "updated", false, "A")
	payload := p0.get("k")

	// p1 停留在 {A:1}，修复到达前被 B 并发更新为 {A:1,B:1}
	p1.writeLocal("k", "old", false, "A")
	p1.writeLocal("k", "concurrent-new", false, "B")

	changed := p1.repair("k", payload) // 条件修复：归并而非覆盖
	fmt.Printf("修复是否改动目标: %v\n", changed)
	fmt.Printf("修复后 p1（两个并发分支都保留）: %v\n", p1.get("k"))

	// 目标已领先时，携带旧值的修复应为无操作
	stale := []Value{{Clock: VClock{"A": 1}, Data: "old", Writer: "A"}}
	fmt.Printf("旧值修复是否改动目标: %v\n", p1.repair("k", stale))
}
