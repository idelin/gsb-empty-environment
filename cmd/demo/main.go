// 运行样例：演示 L0 堆积 4 个 SSTable 触发分层压实的完整过程。
package main

import (
	"fmt"
	"os"

	"lsm"
)

func printLevels(l *lsm.LSM) {
	for lv, files := range l.Levels() {
		if len(files) == 0 {
			continue
		}
		fmt.Printf("  L%d: ", lv)
		for _, f := range files {
			fmt.Printf("%s ", f)
		}
		fmt.Println()
	}
}

func main() {
	dir, err := os.MkdirTemp("", "lsm-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	l, err := lsm.Open(lsm.Options{Dir: dir, L0CompactionTrigger: 4})
	if err != nil {
		panic(err)
	}

	fmt.Println("== 1. L1 铺底 3 个 SSTable（t1[a..c] t2[g..m] t3[x..z]） ==")
	l.AddTable(1, []lsm.Entry{{Seq: 1, Op: lsm.OpPut, Key: "a", Value: []byte("A1")}, {Seq: 2, Op: lsm.OpPut, Key: "c", Value: []byte("C1")}})
	l.AddTable(1, []lsm.Entry{{Seq: 3, Op: lsm.OpPut, Key: "g", Value: []byte("G1")}, {Seq: 4, Op: lsm.OpPut, Key: "m", Value: []byte("M1")}})
	l.AddTable(1, []lsm.Entry{{Seq: 5, Op: lsm.OpPut, Key: "x", Value: []byte("X1")}, {Seq: 6, Op: lsm.OpPut, Key: "z", Value: []byte("Z1")}})
	printLevels(l)

	fmt.Println("\n== 2. 连续 Flush，L0 逐个堆积（Key 范围 [b..h]，与 t1/t2 相交） ==")
	l.Put("b", "B1")
	l.Put("d", "D1")
	l.Put("f", "F0")
	l.Flush()
	fmt.Println("Flush #1 (b, d, f=F0)")
	l.Put("d", "D2")
	l.Put("f", "F1")
	l.Flush()
	fmt.Println("Flush #2 (d=D2, f=F1)")
	l.Del("d")
	l.Put("h", "H1")
	l.Flush()
	fmt.Println("Flush #3 (DEL d, h)")
	printLevels(l)

	fmt.Println("\n== 3. 第 4 次 Flush -> L0 达到 4 个文件，触发 Leveled Compaction ==")
	l.Put("e", "E1")
	before := l.Levels()
	fmt.Printf("压实前 L0 文件数: %d\n", len(before[0]))
	l.Flush()
	fmt.Println("压实后：")
	printLevels(l)

	fmt.Println("\n== 4. 合并产物内容（d 的 Tombstone 已清理，f 只保留最新版 F1） ==")
	for _, f := range l.Levels()[1] {
		entries, err := lsm.DumpTable(f)
		if err != nil {
			panic(err)
		}
		fmt.Printf("  %s:\n", f)
		for _, e := range entries {
			op := "PUT"
			if e.Op == lsm.OpDelete {
				op = "DEL"
			}
			fmt.Printf("    %s %s = %s (seq=%d)\n", op, e.Key, e.Value, e.Seq)
		}
	}

	fmt.Println("\n== 5. 读取验证 ==")
	for _, key := range []string{"a", "d", "f", "x"} {
		if v, ok := l.Get(key); ok {
			fmt.Printf("  Get(%q) = %s\n", key, v)
		} else {
			fmt.Printf("  Get(%q) = <不存在>\n", key)
		}
	}
}
