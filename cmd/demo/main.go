// demo 演示：已提交事务崩溃后存活、未提交事务不暴露、
// 提交点后崩溃由恢复重放、撕裂尾被识别截断。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"pagedb"
)

func show(db *pagedb.DB, pageID uint64) {
	data, ver, err := db.Get(pageID)
	if err != nil {
		log.Fatalf("Get(%d): %v", pageID, err)
	}
	fmt.Printf("  page %d = %q (version %d)\n", pageID, data, ver)
}

func main() {
	dir := "demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	fmt.Println("== 阶段 1: 提交事务 tx1，然后 tx2 未提交即崩溃 ==")
	db, err := pagedb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	tx1, _ := db.Begin()
	tx1.Set(0, []byte("hello"))
	tx1.Set(1, []byte("world"))
	if err := tx1.Commit(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("tx1 已提交: page0=hello page1=world")

	tx2, _ := db.Begin()
	tx2.Set(0, []byte("HALF-TRANSACTION"))
	fmt.Println("tx2 写入 page0=HALF-TRANSACTION 但未提交，模拟进程崩溃")
	db.Close() // Close 不做额外刷盘；tx2 没有 COMMIT 记录

	fmt.Println("== 阶段 2: 重启恢复，只重放有 COMMIT 标记的事务 ==")
	db, err = pagedb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	show(db, 0)
	show(db, 1)

	fmt.Println("== 阶段 3: 提交点后、页刷盘前崩溃（故障注入），恢复重放 ==")
	db.Fail.AfterWALSyncBeforePageFlush = func() error {
		return fmt.Errorf("injected crash")
	}
	tx3, _ := db.Begin()
	tx3.Set(2, []byte("durable-after-recovery"))
	if err := tx3.Commit(); err != nil {
		fmt.Printf("tx3.Commit 返回注入错误（模拟崩溃）: %v\n", err)
	}
	db.Close()
	db, err = pagedb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	show(db, 2)

	fmt.Println("== 阶段 4: 断电造成 WAL 撕裂尾，恢复时识别并截断 ==")
	db.Close()
	walPath := filepath.Join(dir, "wal.log")
	f, _ := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0)
	f.Write([]byte{0x01, 0x02, 0x03, 0x04, 0x05}) // 半条记录
	f.Close()
	db, err = pagedb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("撕裂尾已截断，数据完好:")
	show(db, 0)
	show(db, 2)
	db.Close()
	fmt.Println("演示完成。数据目录:", dir)
}
