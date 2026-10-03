// 压缩位图演示：容器自动选择、批量交并异或、增量快照与检查点归档。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"bitmap"
)

func main() {
	// ---------- 1. 容器自动选择 ----------
	fmt.Println("== 容器自动选择 ==")
	bm := bitmap.New()
	for i := 0; i < 100; i++ {
		bm.Add(uint32(i * 1000)) // 离散稀疏 ID
	}
	bm.AddRange(1<<20, 1<<20+80000) // 密集连续区间
	for i := 0; i < 5000; i++ {     // 密集随机 -> BitmapContainer
		bm.Add(uint32(2<<20 + i))
	}
	s := bm.Stats()
	fmt.Printf("元素总数=%d 容器: 数组=%d 位图=%d RLE=%d\n",
		bm.Cardinality(), s.Array, s.Bitmap, s.Run)

	// ---------- 2. 批量交并异或（微秒级） ----------
	fmt.Println("\n== 批量集合运算 ==")
	a, b := bitmap.New(), bitmap.New()
	const containers = 512 // 512 x 65536 ≈ 3355 万元素 / 位图
	for k := uint64(0); k < containers; k++ {
		a.AddRange(k<<16, (k+1)<<16)
	}
	for k := uint64(0); k < containers; k++ {
		b.AddRange(k<<16+(1<<15), (k+1)<<16+(1<<15)) // 与 a 半重叠
	}
	timeOp := func(name string, f func() *bitmap.Bitmap) {
		start := time.Now()
		r := f()
		fmt.Printf("%s: %8v  结果基数=%d\n", name, time.Since(start), r.Cardinality())
	}
	timeOp("AND", func() *bitmap.Bitmap { return bitmap.And(a, b) })
	timeOp("OR ", func() *bitmap.Bitmap { return bitmap.Or(a, b) })
	timeOp("XOR", func() *bitmap.Bitmap { return bitmap.Xor(a, b) })

	// ---------- 3. 轻量级快照（COW，无停顿） ----------
	fmt.Println("\n== 增量快照与检查点归档 ==")
	start := time.Now()
	snap := bm.Snapshot()
	fmt.Printf("创建快照: %v（仅复制索引，COW 隔离）\n", time.Since(start))

	dir := filepath.Join(os.TempDir(), "bitmap-demo-archive")
	os.RemoveAll(dir)
	cp, err := bitmap.NewCheckpointer(bm, dir)
	must(err)

	chk, err := cp.Checkpoint()
	must(err)
	fmt.Printf("全量检查点: %s (%d 字节)\n", filepath.Base(chk), sizeOf(chk))

	// 模拟业务变更：新增 + 删除。
	bm.AddRange(5<<24, 5<<24+1000000)
	for i := 0; i < 50; i++ {
		bm.Remove(uint32(i * 1000))
	}
	inc, err := cp.Incremental()
	must(err)
	fmt.Printf("增量快照  : %s (%d 字节，仅含变更容器)\n", filepath.Base(inc), sizeOf(inc))

	// 快照不受后续写入影响。
	fmt.Printf("快照基数=%d（隔离） 当前基数=%d\n", snap.Cardinality(), bm.Cardinality())
	snap.Release()

	// ---------- 4. 从归档恢复 ----------
	restored, err := bitmap.Restore(dir)
	must(err)
	diff := bitmap.Xor(bm, restored)
	fmt.Printf("恢复校验  : 与当前位图差异元素=%d（应为 0）\n", diff.Cardinality())
	os.RemoveAll(dir)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func sizeOf(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}
