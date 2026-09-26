// demo 演示分支化字节序列存储的核心能力并打印真实账本数字。
package main

import (
	"fmt"

	"gsb"
)

func main() {
	r := gsb.NewRepo(0)

	fmt.Println("== 1. 构建逻辑序列（跨物理块） ==")
	text := []byte{}
	for i := 0; i < 20; i++ {
		text = append(text, []byte(fmt.Sprintf("record-%03d|", i))...)
	}
	must(r.Insert("main", 0, text))
	logical, _ := r.Len("main")
	fmt.Printf("逻辑长度: %d 字节, 物理块: %d 个, 存活物理占用: %d 字节\n",
		logical, r.Store().LiveBlocks(), r.Store().LiveBytes())

	fmt.Println("\n== 2. 分支快照 + 小编辑（写时复制） ==")
	must(r.Snapshot("v1", "main"))
	before := r.Store().LiveBytes()
	tx := r.Begin()
	must(r.Splice(tx, "main", 5, 1, []byte("X")))
	fmt.Printf("事务内临时复制: %d 块 / %d 字节（已计入物理账本）\n", tx.TempBlocks(), tx.TempBytes())
	must(tx.Commit())
	after := r.Store().LiveBytes()
	fmt.Printf("改 1 字节的物理增量: %d 字节（逻辑序列 %d 字节，未整体复制）\n", after-before, logical)

	fmt.Println("\n== 3. 跨块移动（重叠右值按操作前内容） ==")
	must(r.Move("main", 0, 60, 30))
	got, _ := r.Read("main", 0, 44)
	fmt.Printf("移动后前 44 字节: %s\n", got)
	v1, _ := r.Read("v1", 0, 11)
	fmt.Printf("v1 分支不受影响，前 11 字节: %s\n", v1)

	fmt.Println("\n== 4. 配额超限整体回滚 ==")
	r2 := gsb.NewRepo(r.Store().LiveBytes() + 30)
	must(r2.Insert("main", 0, text))
	err := r2.Insert("main", 0, make([]byte, 500))
	fmt.Printf("超额插入返回: %v\n", err)
	n, _ := r2.Len("main")
	fmt.Printf("回滚后逻辑长度: %d（保持原值 %d）\n", n, len(text))

	fmt.Println("\n== 5. 借用期间底块不可回收 ==")
	r5 := gsb.NewRepo(0)
	must(r5.Insert("main", 0, text))
	must(r5.Snapshot("v1", "main"))
	must(r5.Overwrite("main", 0, make([]byte, len(text)))) // 全量覆盖，旧块仅被 v1 借用
	blocks, freed := r5.Store().Collect()
	fmt.Printf("v1 借用旧块期间 Collect: 回收 %d 块 / %d 字节（旧块被借用，不可回收）\n", blocks, freed)
	must(r5.DropBranch("v1"))
	blocks, freed = r5.Store().Collect()
	fmt.Printf("释放 v1 后 Collect: 回收 %d 块 / %d 字节\n", blocks, freed)

	fmt.Println("\n== 6. 真实编码布局与占用字节 ==")
	enc := r.Encode()
	fmt.Printf("编码占用: %d 字节（布局: 仓库头24 + 块记录(16+负载) + 分支区4 + 分支记录(14+名字) + 跨度记录16）\n", len(enc))
	r3, err := gsb.Decode(enc)
	if err != nil {
		panic(err)
	}
	fmt.Printf("解码往返: 存活物理 %d 字节, 账本核对: ", r3.Store().LiveBytes())
	if err := r3.VerifyLedger(); err != nil {
		panic(err)
	}
	fmt.Println("OK")

	fmt.Println("\n== 7. 独立遍历核对账本 ==")
	if err := r.VerifyLedger(); err != nil {
		panic(err)
	}
	fmt.Printf("存活 %d 字节 / 垃圾 %d 字节 / 编码 %d 字节 — 账本一致\n",
		r.Store().LiveBytes(), r.Store().GarbageBytes(), r.EncodedSize())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
