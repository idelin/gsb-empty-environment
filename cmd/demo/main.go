// demo 演示分层压缩的完整过程：L0 堆积 4 个 SSTable 触发 Compaction，
// L0 全部文件与 L1 中有交集的文件归并，Tombstone 被清理，生成紧凑的 L1 新文件。
package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"leveled"
)

func printLevels(db *leveled.DB) {
	for l, tables := range db.Levels() {
		if len(tables) == 0 {
			fmt.Printf("  L%d: (empty)\n", l)
			continue
		}
		fmt.Printf("  L%d:\n", l)
		for _, t := range tables {
			fmt.Printf("    sst-%d  range=[%s, %s]  size=%dB\n", t.ID, t.MinKey, t.MaxKey, t.Size)
		}
	}
}

func main() {
	dir, err := ioutil.TempDir("", "leveled-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	db, err := leveled.Open(dir, demoOptions())
	if err != nil {
		panic(err)
	}

	fmt.Println("== 1. 预置 L1 三个互不重叠的 SSTable ==")
	must(db.Ingest(1, []leveled.Entry{{Key: "a", Value: []byte("a1")}, {Key: "b", Value: []byte("b1")}, {Key: "c", Value: []byte("c1")}}))
	must(db.Ingest(1, []leveled.Entry{{Key: "e", Value: []byte("e1")}, {Key: "f", Value: []byte("f1")}, {Key: "g", Value: []byte("g1")}, {Key: "h", Value: []byte("h1")}}))
	must(db.Ingest(1, []leveled.Entry{{Key: "x", Value: []byte("x1")}, {Key: "y", Value: []byte("y1")}, {Key: "z", Value: []byte("z1")}}))
	printLevels(db)

	fmt.Println("\n== 2. 连续 Flush 4 次，L0 堆积触发压缩（含更新与删除） ==")
	flushes := [][]leveled.Entry{
		{{Key: "b", Value: []byte("b2")}, {Key: "f", Value: []byte("f2")}}, // 覆盖旧值
		{{Key: "c", Tombstone: true}, {Key: "g", Value: []byte("g2")}},     // 删除 c
		{{Key: "h", Tombstone: true}, {Key: "m", Value: []byte("m1")}},     // 删除 h
		{{Key: "q", Value: []byte("q1")}},
	}
	for i, batch := range flushes {
		must(db.Flush(batch))
		fmt.Printf("-- flush #%d 后:\n", i+1)
		printLevels(db)
	}

	fmt.Println("\n== 3. 压缩结果：L0 全部文件 + L1 中重叠的 [a,c]、[e,h] 归并为一个紧凑 L1 文件；[x,z] 未参与 ==")
	printLevels(db)

	fmt.Println("\n== 4. 可见性验证：新值生效，Tombstone 清理的 Key 不可见 ==")
	for _, k := range []string{"a", "b", "c", "f", "h", "m", "q", "z"} {
		if v, found, _ := get(db, k); found {
			fmt.Printf("  Get(%q) = %s\n", k, v)
		} else {
			fmt.Printf("  Get(%q) = <deleted>\n", k)
		}
	}
}

func demoOptions() leveled.Options {
	return leveled.Options{L0CompactionThreshold: 4, LevelCapacities: []int64{1 << 20, 1 << 20}}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func get(db *leveled.DB, key string) (string, bool, error) {
	v, found, err := db.Get(key)
	if err != nil {
		panic(err)
	}
	return string(v), found, nil
}
