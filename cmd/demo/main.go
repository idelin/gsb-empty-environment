// demo 演示崩溃一致性 VersionSet 的完整流程：
// flush 两个 L0 文件 -> L0->L1 Compaction 写出新文件后"崩溃" ->
// 重启恢复（丢弃孤儿文件）-> 重新执行 Compaction 并成功提交。
package main

import (
	"fmt"
	"log"
	"os"

	vs "versionset"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func show(v *vs.VersionSet, label string) {
	cur := v.Current()
	fmt.Printf("  [%s]\n", label)
	for l := 0; l < vs.NumLevels; l++ {
		for _, f := range cur.Files[l] {
			fmt.Printf("    L%d  %06d.sst  size=%d crc=%08x  [%q,%q]\n",
				l, f.FileNum, f.Size, f.Checksum, f.Smallest, f.Largest)
		}
	}
}

func ls(dir string) {
	entries, _ := os.ReadDir(dir)
	fmt.Print("  磁盘文件: ")
	for _, e := range entries {
		fmt.Print(e.Name(), " ")
	}
	fmt.Println()
}

func flush(v *vs.VersionSet, data string) {
	num := v.NextFileNum()
	meta, err := v.WriteSSTable(num, 0, []byte(data), []byte(data[:1]), []byte(data[len(data)-1:]))
	must(err)
	must(v.LogAndApply(&vs.VersionEdit{NewFiles: []vs.FileMeta{*meta}}))
	fmt.Printf("flush: L0 新增 %06d.sst（已提交 MANIFEST）\n", num)
}

func main() {
	dir := "demo-data"
	os.RemoveAll(dir)

	fmt.Println("== 1. 启动，flush 两个 memtable 到 L0 ==")
	v, err := vs.Open(dir)
	must(err)
	flush(v, "apple")
	flush(v, "banana")
	show(v, "当前版本")
	ls(dir)

	fmt.Println("\n== 2. 开始 L0->L1 Compaction：合并写出新文件并 fsync ==")
	outNum := v.NextFileNum()
	out, err := v.WriteSSTable(outNum, 1, []byte("applebanana"), []byte("a"), []byte("b"))
	must(err)
	fmt.Printf("compaction: 输出 %06d.sst 已落盘，但 VersionEdit 尚未提交\n", out.FileNum)
	ls(dir)

	fmt.Println("\n== 3. *** 崩溃 ***（进程消失，未执行 LogAndApply / Close） ==")
	// 直接遗弃 v，模拟断电。

	fmt.Println("\n== 4. 重启：重放 MANIFEST 恢复版本 ==")
	v2, err := vs.Open(dir)
	must(err)
	show(v2, "恢复后的版本（回到 Compaction 前）")
	ls(dir)
	fmt.Println("  -> 未提交的孤儿 SSTable 已被清理")

	fmt.Println("\n== 5. 重新执行 Compaction，这次成功提交 ==")
	outNum2 := v2.NextFileNum()
	out2, err := v2.WriteSSTable(outNum2, 1, []byte("applebanana"), []byte("a"), []byte("b"))
	must(err)
	cur := v2.Current()
	var dels []vs.DeletedFile
	for _, f := range cur.Files[0] {
		dels = append(dels, vs.DeletedFile{Level: 0, FileNum: f.FileNum})
	}
	must(v2.LogAndApply(&vs.VersionEdit{DeletedFiles: dels, NewFiles: []vs.FileMeta{*out2}}))
	fmt.Printf("compaction: 已提交（L0 删除 %d 个文件，L1 新增 %06d.sst）\n", len(dels), outNum2)
	show(v2, "提交后的版本")
	ls(dir)

	fmt.Println("\n== 6. 再次重启，确认提交后的版本持久化 ==")
	must(v2.Close())
	v3, err := vs.Open(dir)
	must(err)
	defer v3.Close()
	show(v3, "最终版本")
	ls(dir)
}
