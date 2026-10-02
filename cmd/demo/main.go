// demo 演示 ARIES 崩溃恢复与增量快照的完整流程:
// 构造 3 个未提交 + 2 个已提交事务,检查点后崩溃,三阶段恢复。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"aries"
)

func main() {
	dir, err := os.MkdirTemp("", "aries-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	fmt.Println("=== 阶段 0:构造工作负载(5 事务,检查点后崩溃)===")
	db, err := aries.OpenDB(dir, 64)
	must(err)

	t1, _ := db.Begin()
	t2, _ := db.Begin()
	t3, _ := db.Begin()
	t4, _ := db.Begin()
	t5, _ := db.Begin()

	must(db.Update(t1, 1, 0, []byte("t1-before-ckpt")))
	must(db.Update(t3, 2, 0, []byte("t3-uncommitted")))

	ckpt, err := db.Checkpoint()
	must(err)
	fmt.Printf("检查点已归档 @ LSN %d(ATT=%d 事务, DPT=%d 页)\n",
		ckpt, len(db.ActiveTxns()), len(db.BufferPool().DirtyPageTable()))

	// 检查点后立刻做一份增量快照(归档 + 推进磁盘水位)。
	sn, err := aries.NewSnapshotter(db.BufferPool(), db.Log(), filepath.Join(dir, "snaps"))
	must(err)
	meta, err := sn.TakeIncremental()
	must(err)
	fmt.Printf("增量快照 #%d:归档 %d 页,水位 [%d,%d)\n", meta.ID, meta.PageCount, meta.BaseLSN, meta.UpToLSN)

	must(db.Update(t1, 1, 32, []byte("t1-after-ckpt")))
	must(db.Commit(t1))
	must(db.Update(t2, 3, 0, []byte("t2-committed")))
	must(db.Commit(t2))
	must(db.Update(t3, 1, 0, []byte("t3-dirty-write!")))
	must(db.Update(t4, 2, 16, []byte("t4-uncommitted")))
	must(db.Update(t5, 3, 64, []byte("t5-uncommitted")))
	fmt.Println("t1,t2 已提交;t3,t4,t5 未提交")

	must(db.Log().SyncAll())
	must(db.Close()) // 不刷缓冲池 —— 模拟断电
	fmt.Println("*** 崩溃(脏页丢失,日志已持久)***")

	fmt.Println("\n=== 三阶段恢复 ===")
	rdb, res, err := aries.RecoverDB(dir, 64)
	must(err)
	defer rdb.Close()

	fmt.Printf("[Analysis] 检查点 @%d,Redo 起点 @%d\n", res.CheckpointLSN, res.RedoStartLSN)
	fmt.Printf("[Analysis] DPT=%v\n", res.DirtyPageTable)
	fmt.Printf("[Analysis] ATT=%v\n", res.TransactionTable)
	fmt.Printf("[Redo]     重放 %d 条,跳过 %d 条(页已最新)\n", res.RedoneCount, res.SkippedCount)
	fmt.Printf("[Undo]     回滚事务 %v,写入 %d 条 CLR\n", res.UndoneTxns, res.CLRCount)

	fmt.Println("\n=== 恢复后磁盘内容 ===")
	show(rdb, 1, 0, 14, "page1[0:14]  (t1 已提交, t3 脏写已撤销)")
	show(rdb, 1, 32, 13, "page1[32:45] (t1 已提交)")
	show(rdb, 3, 0, 12, "page3[0:12]  (t2 已提交)")
	show(rdb, 2, 0, 15, "page2[0:15]  (t3/t4 未提交 -> 全零)")
	show(rdb, 3, 64, 14, "page3[64:78] (t5 未提交 -> 全零)")

	fmt.Println("\n=== 增量快照归档重放到副本 ===")
	replica := filepath.Join(dir, "replica")
	must(os.MkdirAll(replica, 0o755))
	dm2, err := aries.OpenDiskManager(filepath.Join(replica, "data.db"))
	must(err)
	defer dm2.Close()
	metas, err := aries.RestoreSnapshots(filepath.Join(dir, "snaps"), dm2)
	must(err)
	fmt.Printf("重放 %d 份快照归档到副本,完成\n", len(metas))
}

func show(db *aries.DB, page uint64, off, length int, label string) {
	data, _, err := db.Disk().ReadPage(page)
	must(err)
	fmt.Printf("  %-42s = %q\n", label, string(data[off:off+length]))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
