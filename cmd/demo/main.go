// demo 演示 slotstore：代际句柄、槽复用、崩溃注入与恢复。
// 用法： go run ./cmd/demo <目录>
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"slotstore"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: demo <dir>")
		os.Exit(2)
	}
	dir := os.Args[1]

	fmt.Println("== 1. 打开/创建存储 ==")
	st, err := slotstore.Open(dir, slotstore.Options{SlotSize: 256, SlotCount: 4})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("目录 %s，空闲槽 %d\n", dir, st.FreeCount())

	fmt.Println("\n== 2. 写入两条记录 ==")
	h1, _ := st.Put([]byte("order#1001"))
	h2, _ := st.Put([]byte("order#1002"))
	fmt.Printf("h1=%s h2=%s\n", h1, h2)

	fmt.Println("\n== 3. 删除 h1，同槽写入新对象：代际增加，旧句柄失效 ==")
	st.Delete(h1)
	h3, _ := st.Put([]byte("order#1003"))
	fmt.Printf("新对象 h3=%s（与 h1 同槽，代际 +1）\n", h3)
	if _, err := st.Get(h1); err != nil {
		fmt.Printf("旧句柄 h1 访问 -> %v\n", err)
	}
	if data, err := st.Get(h3); err == nil {
		fmt.Printf("新句柄 h3 访问 -> %q\n", data)
	}
	st.Close()

	fmt.Println("\n== 4. 注入故障：写槽时撕裂写（模拟断电半写槽）后崩溃 ==")
	crash := errors.New("injected crash (power loss)")
	armed := false
	hook := func(op slotstore.FaultOp, buf []byte) ([]byte, error) {
		if armed && op == slotstore.FaultSlotWrite {
			return buf[:len(buf)/2], crash // 只写一半就“断电”
		}
		return buf, nil
	}
	st2, err := slotstore.Open(dir, slotstore.Options{Hook: hook})
	if err != nil {
		log.Fatal(err)
	}
	armed = true // 打开（含恢复）完成后再武装注入点
	if _, err := st2.Put([]byte("order#1004")); err != nil {
		fmt.Printf("Put 中途崩溃 -> %v（进程不 Close，模拟崩溃）\n", err)
	}

	fmt.Println("\n== 5. 崩溃后恢复 ==")
	st3, err := slotstore.Open(dir, slotstore.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer st3.Close()
	fmt.Printf("恢复完成，空闲槽 %d（半写槽已回收）\n", st3.FreeCount())
	if data, err := st3.Get(h2); err == nil {
		fmt.Printf("崩溃前已提交的 h2 仍可读 -> %q\n", data)
	}
	if _, err := st3.Get(h1); err != nil {
		fmt.Printf("旧句柄 h1 依然失效 -> %v\n", err)
	}
}
