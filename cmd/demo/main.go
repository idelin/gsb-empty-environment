// demo 演示 kvstore 的基本用法：追加写、墓碑删除、段轮转、压缩与重启恢复。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"kvstore/kvstore"
)

func listFiles(dir string) string {
	ents, _ := os.ReadDir(dir)
	out := ""
	for _, e := range ents {
		if out != "" {
			out += " "
		}
		out += e.Name()
	}
	return out
}

func main() {
	dir := filepath.Join(os.TempDir(), "kvstore-demo")
	os.RemoveAll(dir)

	// MaxSegmentBytes 调小以便演示段轮转；SyncWrites 打开保证每次写落盘。
	s, err := kvstore.Open(dir, &kvstore.Options{MaxSegmentBytes: 128, SyncWrites: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("== 打开存储:", dir)

	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	get := func(key string) string {
		v, err := s.Get([]byte(key))
		if err == kvstore.ErrNotFound {
			return "<不存在>"
		}
		if err != nil {
			log.Fatal(err)
		}
		return string(v)
	}

	// 同键 写 -> 删 -> 写。
	must(s.Put([]byte("name"), []byte("alice")))
	must(s.Put([]byte("city"), []byte("beijing")))
	must(s.Delete([]byte("name")))
	must(s.Put([]byte("name"), []byte("ada")))
	for i := 0; i < 12; i++ { // 制造垃圾并触发段轮转
		must(s.Put([]byte(fmt.Sprintf("tmp%02d", i)), []byte("junk-value")))
		must(s.Delete([]byte(fmt.Sprintf("tmp%02d", i))))
	}
	fmt.Printf("== 写删写后: name=%q city=%q\n", get("name"), get("city"))
	fmt.Println("== 轮转而产生的文件:", listFiles(dir))

	must(s.Compact())
	fmt.Println("== 压缩后的文件:", listFiles(dir))
	fmt.Printf("== 压缩后读取: name=%q city=%q (可见键数 %d)\n", get("name"), get("city"), s.Len())

	// 模拟进程崩溃后直接重启：扫描各段重建索引。
	must(s.Close())
	s, err = kvstore.Open(dir, &kvstore.Options{MaxSegmentBytes: 128, SyncWrites: true})
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	fmt.Printf("== 重启恢复后: name=%q city=%q (可见键数 %d)\n", get("name"), get("city"), s.Len())
}
