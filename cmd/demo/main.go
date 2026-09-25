// demo 演示 pagealloc 的申请、释放、事务回滚、故障注入崩溃与恢复。
package main

import (
	"flag"
	"fmt"
	"log"

	"pagealloc"
)

func show(a *pagealloc.Allocator, label string) {
	fmt.Printf("%-28s 空闲区间=%v 空闲页=%d 占用页=%d 总页数=%d\n",
		label, a.FreeRanges(), a.FreeCount(), a.AllocatedCount(), a.Total())
}

func main() {
	dir := flag.String("dir", "./pagedata", "状态文件所在目录（由调用方指定）")
	flag.Parse()

	// 1. 创建分配器：16 页，全部空闲。
	a, err := pagealloc.Open(*dir, 16)
	if err != nil {
		log.Fatal(err)
	}
	show(a, "创建 16 页")

	// 2. 事务 1：申请 3 页后回滚 —— 页可重用。
	tx, _ := a.Begin()
	p, _ := tx.Alloc(3)
	fmt.Printf("事务1 申请 3 页 -> 起始页 %d，随后回滚\n", p)
	tx.Rollback()
	show(a, "回滚后（3 页可重用）")

	// 3. 事务 2：申请 3 页 + 4 页并提交。
	tx2, _ := a.Begin()
	p1, _ := tx2.Alloc(3)
	p2, _ := tx2.Alloc(4)
	fmt.Printf("事务2 申请 -> 页[%d,%d) 与 页[%d,%d)，提交\n", p1, p1+3, p2, p2+4)
	if err := tx2.Commit(); err != nil {
		log.Fatal(err)
	}
	show(a, "提交后")

	// 4. 双重释放与越界释放被拒绝。
	tx3, _ := a.Begin()
	if err := tx3.Free(0, 3); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("重复释放 [0,3) -> %v\n", tx3.Free(0, 3))
	fmt.Printf("越界释放 [14,18) -> %v\n", tx3.Free(14, 4))
	tx3.Commit()
	show(a, "释放 [0,3) 并提交后")

	// 5. 注入故障模拟崩溃：在 rename 前“断电”。
	a.Injector().Arm(pagealloc.FaultBeforeRename)
	tx4, _ := a.Begin()
	tx4.Alloc(2)
	err = tx4.Commit()
	fmt.Printf("注入故障后提交 -> %v（模拟进程崩溃）\n", err)

	// 6. 模拟重启：从目录恢复，状态回到最近一次成功提交，总页数守恒。
	a2, err := pagealloc.Open(*dir, 0)
	if err != nil {
		log.Fatal(err)
	}
	show(a2, "崩溃恢复后")
	fmt.Printf("页数守恒: %d + %d = %d\n", a2.FreeCount(), a2.AllocatedCount(), a2.Total())
}
