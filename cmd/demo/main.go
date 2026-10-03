// 运行样例：两个节点并发互移目录，合并后自动仲裁断环。
package main

import (
	"fmt"

	"crdtmove/crdt"
)

func main() {
	// 两个副本引导出相同的初始树：/dirA 与 /dirB。
	n1, n2 := crdt.NewTree("node1"), crdt.NewTree("node2")
	for _, tr := range []*crdt.Tree{n1, n2} {
		tr.Mkdir(crdt.RootID, "dirA", "dirA")
		tr.Mkdir(crdt.RootID, "dirB", "dirB")
	}
	fmt.Println("== 初始状态 ==")
	fmt.Print(n1)

	// 并发操作（各自本地提交，尚未通信）。
	op1, _ := n1.Move("dirA", "dirB") // 节点1: /dirA -> /dirB/dirA
	op2, _ := n2.Move("dirB", "dirA") // 节点2: /dirB -> /dirA/dirB
	fmt.Printf("节点1 提交: %s\n", op1)
	fmt.Printf("节点2 提交: %s\n", op2)

	// 合并：双向交换操作日志。
	n1.Merge(n2.Ops())
	n2.Merge(n1.Ops())

	fmt.Println("\n== 合并后（两副本收敛） ==")
	fmt.Print(n1)
	for _, op := range n1.Undone() {
		fmt.Printf("仲裁撤销（断环）: %s\n", op)
	}
	pA, _ := n1.Path("dirA")
	pB, _ := n1.Path("dirB")
	fmt.Printf("dirA 最终路径: %s\ndirB 最终路径: %s\n", pA, pB)
	fmt.Printf("无环校验: %v, 目录完整: dirA=%v dirB=%v\n",
		n1.IsAcyclic(), n1.Contains("dirA"), n1.Contains("dirB"))
}
