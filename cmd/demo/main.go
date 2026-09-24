// demo 演示 treeindex 的用法：建树、原子移动、循环/重名拒绝、
// 重命名保持 ID、以及崩溃恢复。
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"treeindex"
)

func printTree(ix *treeindex.Index) {
	for _, id := range ix.IDs() {
		p, err := ix.Path(id)
		if err != nil {
			continue
		}
		fmt.Printf("    %-6s -> %s\n", id, p)
	}
}

func main() {
	dir := "demodata"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir) // 可重复运行

	fmt.Println("== 1. 打开索引并建树（批次原子提交）==")
	ix, err := treeindex.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	err = ix.Commit([]treeindex.Op{
		treeindex.Create("docs", treeindex.RootID, "docs"),
		treeindex.Create("a", "docs", "a"),
		treeindex.Create("b", "a", "b"), // b 是 a 的子目录
		treeindex.Create("pic", "b", "pic.png"),
		treeindex.Create("arc", treeindex.RootID, "archive"),
	})
	if err != nil {
		log.Fatal(err)
	}
	printTree(ix)

	fmt.Println("== 2. 拒绝把 A 移到它自己的子目录 B（循环检测）==")
	if err := ix.Commit([]treeindex.Op{treeindex.Move("a", "b")}); err != nil {
		fmt.Println("    rejected:", err)
	}
	fmt.Println("    路径保持原样:")
	printTree(ix)

	fmt.Println("== 3. 同批兄弟重名：与批次顺序无关，整批拒绝 ==")
	err = ix.Commit([]treeindex.Op{
		treeindex.Create("n1", "docs", "dup"),
		treeindex.Create("n2", "docs", "dup"),
	})
	fmt.Println("    rejected:", err)
	if _, ok := ix.Node("n1"); !ok {
		fmt.Println("    n1 未残留（批次原子性）")
	}

	fmt.Println("== 4. 原子移动子树 + 重命名（ID 不变，缓存按版本失效）==")
	fmt.Printf("    version before: %d\n", ix.Version())
	if err := ix.Commit([]treeindex.Op{
		treeindex.Move("a", "arc"),
		treeindex.Rename("a", "a2"),
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("    version after:  %d\n", ix.Version())
	printTree(ix)

	fmt.Println("== 5. 模拟崩溃：日志已提交、写快照前断电 ==")
	crash := errors.New("simulated power loss")
	ix2, err := treeindex.Open(dir,
		treeindex.WithHook(treeindex.HookBeforeSnapshotWrite, func() error { return crash }))
	if err != nil {
		log.Fatal(err)
	}
	err = ix2.Commit([]treeindex.Op{treeindex.Create("note", "arc", "note.txt")})
	fmt.Println("    commit returned:", err)

	fmt.Println("== 6. 进程重启：Open 重放 WAL，已提交批次恢复 ==")
	ix3, err := treeindex.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	printTree(ix3)
}
