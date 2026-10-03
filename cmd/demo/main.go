// demo 演示 Tombstone 的完整生命周期:
// 写入 -> 删除(写 Tombstone)-> 读屏蔽 -> 逐层下沉 -> Bottom-most 物理擦除。
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"example.com/tombstone-lsm/lsm"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func printStats(db *lsm.DB) {
	for _, s := range db.Stats() {
		fmt.Printf("    L%d: tables=%d entries=%d\n", s.Level, s.Tables, s.Entries)
	}
}

func printProbe(db *lsm.DB, key string) {
	probe, err := db.Probe(key)
	must(err)
	if len(probe) == 0 {
		fmt.Printf("    Probe(%s): 磁盘上已无任何记录(物理擦除完成)\n", key)
		return
	}
	for _, p := range probe {
		where := fmt.Sprintf("L%d", p.Level)
		if p.Level == -1 {
			where = "Memtable"
		}
		fmt.Printf("    Probe(%s): 位于 %-8s tombstone=%v seq=%d\n", key, where, p.Tombstone, p.Seq)
	}
}

func main() {
	dir := "./demo-data"
	os.RemoveAll(dir)
	defer os.RemoveAll(dir)

	db, err := lsm.Open(dir, &lsm.Options{
		MemtableFlushThreshold: 1000, // 演示中手动 Flush
		MaxLevels:              4,    // L0..L3,L3 为 Bottom-most
		Level0Capacity:         2,
		LevelSizeRatio:         2,
		AutoCompact:            false, // 手动逐步驱动,便于观察
	})
	must(err)
	defer db.Close()

	fmt.Println("== 1. 插入 K1=V1(附带若干其他键)并 Flush ==")
	must(db.Put("K1", []byte("V1")))
	must(db.Put("A1", []byte("a")))
	must(db.Flush())
	printStats(db)
	printProbe(db, "K1")

	fmt.Println("== 2. 删除 K1(写入 Tombstone)并 Flush ==")
	must(db.Delete("K1"))
	must(db.Put("B2", []byte("b")))
	must(db.Flush())
	printStats(db)
	printProbe(db, "K1")

	fmt.Println("== 3. Point Get:K1 立即被 Tombstone 屏蔽 ==")
	_, err = db.Get("K1")
	fmt.Printf("    Get(K1) -> %v\n", err)

	fmt.Println("== 4. Range Scan:结果中不包含 K1 ==")
	kvs, err := db.Scan("", "")
	must(err)
	for _, kv := range kvs {
		fmt.Printf("    %s=%s\n", kv.Key, kv.Value)
	}

	fmt.Println("== 5. 多轮 Compaction:Tombstone 逐层下沉,直到底层被擦除 ==")
	filler := 0
	for step := 0; step < 100; step++ {
		merged, err := db.CompactOnce()
		must(err)
		if !merged {
			// 没有可合并的层,写入新数据制造 L0 压力
			filler++
			must(db.Put(fmt.Sprintf("F%02d", filler), []byte("x")))
			must(db.Put(fmt.Sprintf("G%02d", filler), []byte("y")))
			must(db.Flush())
			continue
		}
		fmt.Printf("  -- 合并发生后 --\n")
		printStats(db)
		printProbe(db, "K1")
		probe, err := db.Probe("K1")
		must(err)
		if len(probe) == 0 {
			break
		}
	}

	fmt.Println("== 6. 物理验证:扫描所有 SSTable 文件的原始字节 ==")
	found := false
	fis, err := ioutil.ReadDir(dir)
	must(err)
	for _, fi := range fis {
		if !strings.HasSuffix(fi.Name(), ".sst") {
			continue
		}
		data, err := ioutil.ReadFile(filepath.Join(dir, fi.Name()))
		must(err)
		if strings.Contains(string(data), "K1") {
			found = true
			fmt.Printf("    %s 中仍包含 \"K1\"\n", fi.Name())
		}
	}
	fmt.Printf("    磁盘文件中残留 \"K1\": %v\n", found)

	_, err = db.Get("K1")
	fmt.Printf("    最终 Get(K1) -> %v\n", err)
	kvs, err = db.Scan("", "")
	must(err)
	fmt.Printf("    最终 Scan 共 %d 个存活键,K1 不在其中\n", len(kvs))
}
