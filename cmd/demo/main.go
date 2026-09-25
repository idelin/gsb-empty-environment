// demo 演示 mvcc 存储的核心行为：版本化读写、快照隔离、
// 墓碑删除、受最老读者限制的 GC，以及崩溃恢复。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"mvcc"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func put(s *mvcc.Store, key, val string) {
	tx, err := s.Begin()
	must(err)
	must(tx.Put(key, []byte(val)))
	must(tx.Commit())
}

func get(s *mvcc.Store, r *mvcc.ReadTx, key string) string {
	v, ok, err := r.Get(key)
	must(err)
	if !ok {
		return "<不存在>"
	}
	return string(v)
}

func main() {
	dir := filepath.Join(os.TempDir(), "mvcc-demo")
	os.RemoveAll(dir)

	s, err := mvcc.Open(dir, mvcc.Options{SyncWrites: true})
	must(err)

	put(s, "name", "alice") // 版本 1
	r1, err := s.Snapshot() // 长读事务，固定在版本 1
	must(err)
	put(s, "name", "bob") // 版本 2：长读不阻塞新写
	tx, err := s.Begin()  // 版本 3：删除产生墓碑
	must(err)
	must(tx.Delete("name"))
	must(tx.Commit())

	r2, err := s.Snapshot()
	must(err)
	fmt.Printf("目录: %s\n", dir)
	fmt.Printf("旧读者(固定v%d) 读 name = %s\n", r1.Version(), get(s, r1, "name"))
	fmt.Printf("新读者(固定v%d) 读 name = %s\n", r2.Version(), get(s, r2, "name"))

	freed, err := s.GC()
	must(err)
	fmt.Printf("旧读者存活时 GC 释放条目数 = %d（受最老活跃读版本限制）\n", freed)

	must(r1.Close()) // 关闭旧读者后才可释放链前缀
	freed, err = s.GC()
	must(err)
	fmt.Printf("旧读者关闭后 GC 释放条目数 = %d\n", freed)

	sizeBefore, _ := s.LogSize()
	must(s.Compact())
	sizeAfter, _ := s.LogSize()
	fmt.Printf("Compact 日志大小: %d -> %d 字节\n", sizeBefore, sizeAfter)
	must(r2.Close())
	must(s.Close())

	// 模拟进程重启：重放日志恢复。
	s2, err := mvcc.Open(dir, mvcc.Options{SyncWrites: true})
	must(err)
	defer s2.Close()
	r3, err := s2.Snapshot()
	must(err)
	defer r3.Close()
	fmt.Printf("重启恢复后: 版本 = %d, name = %s\n", s2.Version(), get(s2, r3, "name"))
}
