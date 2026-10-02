// demo 演示 B+ 树的级联收缩与物理合并：
// 从 500 条记录的满树（4 层）逐一删除记录至剩余 3 条，
// 观察树高由 4 层缩减至 1 层，空余页面回收至空闲链表。
package main

import (
	"fmt"

	"bptree"
)

func main() {
	const total = 500
	keys := make([]int, total)
	for i := range keys {
		keys[i] = i + 1
	}

	tr := bptree.BulkLoad(keys)
	fmt.Printf("批量加载 %d 条记录：树高=%d，在用页面=%d，空闲链表=%d\n",
		total, tr.Height(), tr.ActivePages(), tr.FreeListLen())

	fmt.Println("开始逐一删除 1..497 ...")
	prev := tr.Height()
	for k := 1; k <= total-3; k++ {
		tr.Delete(k)
		if h := tr.Height(); h != prev {
			fmt.Printf("  删除键 %3d 后：树高 %d -> %d（在用页面=%3d，已回收=%3d，合并=%3d，借调=%3d）\n",
				k, prev, h, tr.ActivePages(), tr.TotalFrees(), tr.Merges, tr.Borrows)
			prev = h
		}
	}

	fmt.Println("删除完成：")
	fmt.Printf("  剩余键        = %v\n", tr.Keys())
	fmt.Printf("  树高          = %d\n", tr.Height())
	fmt.Printf("  在用页面      = %d\n", tr.ActivePages())
	fmt.Printf("  空闲链表页面  = %d\n", tr.FreeListLen())
	fmt.Printf("  物理合并次数  = %d\n", tr.Merges)
	fmt.Printf("  兄弟借调次数  = %d\n", tr.Borrows)
	if err := tr.Validate(); err != nil {
		fmt.Println("结构校验：失败 -", err)
	} else {
		fmt.Println("结构校验：通过")
	}
}
