// demo 演示 diskbtree 的基本用法: 打开目录、插入、范围扫描、
// 模拟崩溃恢复与快照。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"diskbtree"
)

func main() {
	dir := filepath.Join(os.TempDir(), "diskbtree-demo")
	os.RemoveAll(dir)

	// 文件全部放在调用方指定的目录下: data.db(页数据) + meta(superblock)。
	db, err := diskbtree.Open(dir, &diskbtree.Options{PageSize: 256})
	if err != nil {
		log.Fatal(err)
	}

	for i := 1; i <= 20; i++ {
		k := fmt.Sprintf("user:%03d", i)
		if err := db.Put([]byte(k), []byte("payload-"+k)); err != nil {
			log.Fatal(err)
		}
	}
	// 重复键覆盖
	if err := db.Put([]byte("user:007"), []byte("payload-updated")); err != nil {
		log.Fatal(err)
	}

	fmt.Println("== 范围扫描 [user:005, user:010) ==")
	it := db.Scan([]byte("user:005"), []byte("user:010"))
	for it.Next() {
		fmt.Printf("  %s => %s\n", it.Key(), it.Value())
	}
	if err := it.Err(); err != nil {
		log.Fatal(err)
	}

	// 快照: 之后的写入(包括根分裂)不影响它。
	snap := db.Snapshot()
	for i := 21; i <= 60; i++ {
		k := fmt.Sprintf("user:%03d", i)
		if err := db.Put([]byte(k), []byte("payload-"+k)); err != nil {
			log.Fatal(err)
		}
	}
	cnt := 0
	for it := snap.Scan(nil, nil); it.Next(); {
		cnt++
	}
	fmt.Printf("== 旧快照键数(应为20): %d ==\n", cnt)
	db.Close()

	// 模拟进程重启: 重新 Open 即完成崩溃恢复。
	db2, err := diskbtree.Open(dir, &diskbtree.Options{PageSize: 256})
	if err != nil {
		log.Fatal(err)
	}
	defer db2.Close()
	v, ok, err := db2.Get([]byte("user:007"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("== 重开后 Get(user:007): ok=%v val=%s ==\n", ok, v)
	cnt = 0
	for it := db2.Scan(nil, nil); it.Next(); {
		cnt++
	}
	fmt.Printf("== 重开后全量扫描键数(应为60): %d ==\n", cnt)
	fmt.Println("数据目录:", dir)
}
