// pagedemo 演示 pagealloc 的基本用法与崩溃恢复。
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"pagealloc"
)

func main() {
	dir, err := os.MkdirTemp("", "pagedemo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fmt.Println("数据目录:", dir)

	a, err := pagealloc.Open(pagealloc.Options{Dir: dir, PageCount: 16, PageSize: 4096})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("新建分配器: 总页数=%d 页大小=%dB 空闲=%d\n", a.PageCount(), a.PageSize(), a.FreeCount())

	// 1. 申请 3 页后回滚 -> 可重用
	tx := a.Begin()
	p, _ := tx.Alloc(3)
	tx.Rollback()
	fmt.Printf("申请 3 页 [%d,%d) 后回滚, 空闲=%d (页可重用)\n", p, p+3, a.FreeCount())

	// 2. 正式申请两段
	tx = a.Begin()
	x, _ := tx.Alloc(3)
	y, _ := tx.Alloc(2)
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("提交: 段A=[%d,%d) 段B=[%d,%d), 空闲=%d\n", x, x+3, y, y+2, a.FreeCount())

	// 3. 释放相邻区间 -> 合并
	tx = a.Begin()
	must(tx.FreeRange(x, 3))
	must(tx.FreeRange(y, 2))
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	tx = a.Begin()
	z, err := tx.Alloc(5)
	must(err)
	tx.Rollback()
	fmt.Printf("释放相邻段后合并, 可一次申请 5 连续页 [%d,%d)\n", z, z+5)

	// 4. 非法操作: 已占用页 6 先申请出来, 再重复释放
	tx = a.Begin()
	occ, _ := tx.Alloc(1)
	if err := tx.Commit(); err != nil {
		log.Fatal(err)
	}
	tx = a.Begin()
	fmt.Printf("释放已占用页 %d -> %v; 再次释放 -> %v\n", occ, tx.Free(occ), tx.Free(occ))
	fmt.Println("越界释放页 100 ->", tx.Free(100))
	tx.Rollback()

	// 5. 注入故障模拟崩溃: commit 记录落盘后、状态落盘前断电
	crash := errors.New("simulated power loss")
	hooks := &pagealloc.Hooks{AfterJournalCommitSync: func() error { return crash }}
	a2, err := pagealloc.Open(pagealloc.Options{Dir: dir, Hooks: hooks})
	if err != nil {
		log.Fatal(err)
	}
	tx = a2.Begin()
	v, _ := tx.Alloc(4)
	err = tx.Commit()
	fmt.Printf("提交 4 页 [%d,%d) 时崩溃: %v\n", v, v+4, err)

	// 6. 重启恢复: 已提交事务重放, 页数守恒
	a3, err := pagealloc.Open(pagealloc.Options{Dir: dir})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("重启恢复后: 空闲=%d (事务已重放, 总页数=%d 守恒)\n", a3.FreeCount(), a3.PageCount())
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
