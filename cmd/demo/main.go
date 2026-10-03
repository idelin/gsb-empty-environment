// demo 复现验收场景: 两个节点并发交叉移动目录, 合并后自动断环。
package main

import (
	"fmt"

	"treecrdt/crdt"
)

func printTree(r *crdt.Replica, label string) {
	fmt.Printf("--- %s ---\n", label)
	for _, id := range []crdt.NodeID{"dirA", "dirB", "fileA", "fileB"} {
		if p, ok := r.Path(id); ok {
			fmt.Printf("  %-6s -> %s\n", id, p)
		}
	}
}

func main() {
	base := crdt.NewBase(map[crdt.NodeID]crdt.BaseNode{
		crdt.RootID: {Name: "", Parent: ""},
		"dirA":      {Name: "dirA", Parent: crdt.RootID},
		"dirB":      {Name: "dirB", Parent: crdt.RootID},
		"fileA":     {Name: "a.txt", Parent: "dirA"},
		"fileB":     {Name: "b.txt", Parent: "dirB"},
	})

	node1 := crdt.NewReplica("node1", base)
	node2 := crdt.NewReplica("node2", base)

	fmt.Println("== 初始状态 ==")
	printTree(node1, "node1 / node2 (一致)")

	// 并发操作(双方 Lamport 时钟均为 1, 互不可见)
	ts1 := node1.Move("dirA", "dirB", "") // 节点1: /dirA -> /dirB/dirA
	ts2 := node2.Move("dirB", "dirA", "") // 节点2: /dirB -> /dirA/dirB
	fmt.Printf("\n== 并发操作 ==\n  node1: move dirA -> dirB   (ts %s)\n  node2: move dirB -> dirA   (ts %s)\n", ts1, ts2)

	fmt.Println("\n== 合并前各自的本地视图 ==")
	printTree(node1, "node1 本地")
	printTree(node2, "node2 本地")

	// 双向合并
	node1.Merge(node2)
	node2.Merge(node1)

	fmt.Println("\n== 合并后(两副本已收敛, 拓扑环被自动仲裁) ==")
	printTree(node1, "node1")
	printTree(node2, "node2")

	fmt.Println("\n== 仲裁日志(被确定性撤销的操作) ==")
	for _, rej := range node1.Rejected() {
		fmt.Printf("  撤销 move %s -> %s (ts %s)\n    原因: %s\n",
			rej.Op.Node, rej.Op.NewParent, rej.Op.Time, rej.Reason)
	}

	fmt.Println("\n== 不变式检查 ==")
	fmt.Printf("  视图无环:            %v\n", !node1.HasCycle() && !node2.HasCycle())
	fmt.Printf("  两副本视图完全一致:  %v\n", fmt.Sprint(node1.Snapshot()) == fmt.Sprint(node2.Snapshot()))
}
