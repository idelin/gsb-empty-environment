// voldemo 演示 volmap 的写入、跨界写、打洞、快照与崩溃恢复。
// 用法: go run ./cmd/voldemo [目录]（默认 ./vol-data）
package main

import (
	"fmt"
	"log"
	"os"

	"volmap"
)

func show(v *volmap.Volume, label string, n int) {
	buf := make([]byte, n)
	if _, err := v.Read(buf, 0); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%-28s %q\n", label, string(buf))
}

func main() {
	dir := "./vol-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	v, err := volmap.Open(dir, nil)
	if err != nil {
		log.Fatal(err)
	}

	must(v.Write(0, []byte("AAAAAAAAAAAAAAAA")))  // [0,16)
	must(v.Write(16, []byte("BBBBBBBBBBBBBBBB"))) // [16,32)
	show(v, "两个块写入后:", 32)

	must(v.Write(12, []byte("xxxx"))) // 跨界写 [12,16)，只改覆盖区
	show(v, "跨界写 [12,16) 后:", 32)

	snap, err := v.Snapshot()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("打快照: id=%d\n", snap)

	must(v.PunchHole(8, 16)) // 打洞中段的洞 [8,24)
	show(v, "打洞 [8,24) 后(洞读为0):", 32)

	snapBuf := make([]byte, 32)
	if _, err := v.ReadSnapshot(snap, snapBuf, 0); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%-28s %q\n", "旧快照不受打洞影响:", string(snapBuf))

	// 模拟进程重启：重新 Open 执行崩溃恢复。
	v2, err := volmap.Open(dir, nil)
	if err != nil {
		log.Fatal(err)
	}
	show(v2, "重开后(崩溃恢复)状态:", 32)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
