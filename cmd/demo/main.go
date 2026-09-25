// demo 演示双槽根指针发布：真实目录提交/重开，以及注入故障后的恢复。
package main

import (
	"fmt"
	"os"

	"rootslot"
)

func main() {
	dir := os.TempDir() + "/rootslot-demo"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	pages := rootslot.MemPages{
		1: rootslot.InternalPage(2, 3),
		2: rootslot.LeafPage(),
		3: rootslot.LeafPage(),
	}
	v := &rootslot.PageValidator{Pages: pages}

	fmt.Printf("== 真实目录 %s 提交并崩溃重开 ==\n", dir)
	store, err := rootslot.Open(dir, v)
	must(err)
	for _, id := range []uint64{2, 3, 1} {
		must(store.Commit(rootslot.RootDescriptor(id)))
		fmt.Printf("committed root page %d (seq=%d)\n", id, store.Seq())
	}
	store.Close()

	reopened, err := rootslot.Open(dir, v)
	must(err)
	fmt.Printf("reopened: seq=%d, verify closure: %v\n", reopened.Seq(), reopened.Verify())

	fmt.Println("== 注入故障：新槽撕裂写，断电后恢复旧根 ==")
	sim := rootslot.NewSimBackend()
	sim.TornAt = map[int]int{1: 137} // 第二次提交只落盘前 137 字节
	s2, err := rootslot.OpenBackend(sim, nil)
	must(err)
	must(s2.Commit([]byte("root-old")))
	must(s2.Commit([]byte("root-new")))
	sim.Crash(true) // 断电
	recovered, err := rootslot.OpenBackend(sim, nil)
	must(err)
	fmt.Printf("recovered root: %q (diagnostics: %v)\n", recovered.Root(), recovered.Diagnostics())

	fmt.Println("== 两槽都损坏：拒绝打开 ==")
	sim2 := rootslot.NewSimBackend()
	sim2.WriteAt(make([]byte, rootslot.SlotSize), 0) // 全零是 empty，写入垃圾才是 corrupt
	garbage := []byte("garbage-slot-not-a-valid-header")
	sim2.WriteAt(garbage, 0)
	sim2.WriteAt(garbage, rootslot.SlotSize)
	sim2.Sync()
	sim2.Crash(true)
	_, err = rootslot.OpenBackend(sim2, nil)
	fmt.Printf("open with both slots corrupt: %v\n", err)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
