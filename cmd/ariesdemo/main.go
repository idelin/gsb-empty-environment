// Command ariesdemo builds a WAL with 5 transactions (2 committed, 3 left
// uncommitted), crashes after a checkpoint, then runs the three ARIES
// recovery phases and prints each step.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aries"
)

func main() {
	dir, err := os.MkdirTemp("", "ariesdemo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "wal.log")
	dbPath := filepath.Join(dir, "db.dat")

	fmt.Println("========== 1. 正常执行：5 个事务写 WAL ==========")
	wal, err := aries.CreateWAL(walPath)
	must(err)
	ps, err := aries.OpenPageStore(dbPath)
	must(err)

	prev := map[uint64]uint64{}
	update := func(txn uint64, page uint32, off uint32, data string) uint64 {
		old, _ := ps.ReadAt(page, off, len(data))
		rec := &aries.Record{Type: aries.TypeUpdate, TxnID: txn, PrevLSN: prev[txn],
			PageID: page, Offset: off, OldData: old, NewData: []byte(data)}
		lsn, err := wal.Append(rec)
		must(err)
		prev[txn] = lsn
		must(ps.ApplyUpdate(page, off, []byte(data), lsn))
		fmt.Printf("  LSN=%-4d T%d UPDATE P%d[%d] %q -> %q\n", lsn, txn, page, off, old, data)
		return lsn
	}
	txnRec := func(typ byte, txn uint64, name string) uint64 {
		lsn, err := wal.Append(&aries.Record{Type: typ, TxnID: txn, PrevLSN: prev[txn]})
		must(err)
		prev[txn] = lsn
		fmt.Printf("  LSN=%-4d T%d %s\n", lsn, txn, name)
		return lsn
	}

	t1u1 := update(1, 1, 0, "A1")
	t2u1 := update(2, 2, 0, "B1")
	t2commit := txnRec(aries.TypeCommit, 2, "COMMIT")
	t3u1 := update(3, 1, 8, "C1")

	fmt.Println("========== 2. Checkpoint（TT + DPT 快照落盘） ==========")
	beginLSN, err := wal.Append(&aries.Record{Type: aries.TypeBeginCheckpoint})
	must(err)
	fmt.Printf("  LSN=%-4d BEGIN_CHECKPOINT\n", beginLSN)
	ckptLSN, err := wal.Append(&aries.Record{
		Type: aries.TypeEndCheckpoint, BeginLSN: beginLSN,
		Txns: []aries.TxnSnapshot{
			{TxnID: 1, LastLSN: t1u1, Status: byte(aries.StatusRunning)},
			{TxnID: 2, LastLSN: t2commit, Status: byte(aries.StatusCommitted)},
			{TxnID: 3, LastLSN: t3u1, Status: byte(aries.StatusRunning)},
		},
		DirtyPages: []aries.DirtyPageSnapshot{
			{PageID: 1, RecLSN: t1u1},
			{PageID: 2, RecLSN: t2u1},
		},
	})
	must(err)
	fmt.Printf("  LSN=%-4d END_CHECKPOINT  TT={T1:RUNNING T2:COMMITTED T3:RUNNING} DPT={P1:%d P2:%d}\n",
		ckptLSN, t1u1, t2u1)

	must(ps.FlushPage(1))
	must(ps.FlushPage(2))
	fmt.Println("  [buffer pool] 仅刷盘 P1、P2（P3 从未落盘）")

	fmt.Println("========== 3. Checkpoint 之后继续执行，然后崩溃 ==========")
	txnRec(aries.TypeEnd, 2, "END")
	update(4, 3, 0, "D1")
	txnRec(aries.TypeCommit, 4, "COMMIT")
	update(5, 2, 8, "E1")
	update(1, 3, 8, "A2")
	update(3, 1, 16, "C2")
	must(wal.Sync())
	wal.Close()
	fmt.Println("  *** 崩溃！T1/T3/T5 未提交，内存中的脏页全部丢失 ***")

	fmt.Println("========== 4. Analysis：重建 TT 与 DPT ==========")
	r, err := aries.NewRecovery(walPath, dbPath)
	must(err)
	r.Analysis()
	var txnIDs []int
	for id := range r.TT {
		txnIDs = append(txnIDs, int(id))
	}
	sort.Ints(txnIDs)
	for _, id := range txnIDs {
		e := r.TT[uint64(id)]
		fmt.Printf("  TT  T%-4d %-10s lastLSN=%d\n", id, e.Status, e.LastLSN)
	}
	var pages []int
	for p := range r.DPT {
		pages = append(pages, int(p))
	}
	sort.Ints(pages)
	for _, p := range pages {
		fmt.Printf("  DPT P%-4d recLSN=%d\n", p, r.DPT[uint32(p)])
	}
	var losers []string
	for _, e := range r.Losers() {
		losers = append(losers, fmt.Sprintf("T%d", e.TxnID))
	}
	fmt.Printf("  败者事务(losers): %s\n", strings.Join(losers, " "))

	fmt.Println("========== 5. Redo：历史重复 ==========")
	must(r.Redo())
	for _, p := range []uint32{1, 2, 3} {
		fmt.Printf("  P%d pageLSN=%-4d [0:2]=%q [8:10]=%q [16:18]=%q\n",
			p, r.Pages.PageLSN(p),
			read(r.Pages, p, 0), read(r.Pages, p, 8), read(r.Pages, p, 16))
	}

	fmt.Println("========== 6. Undo：反向扫描，写 CLR 补偿 ==========")
	must(r.Undo())
	img, err := aries.MapWAL(walPath)
	must(err)
	img.ScanFrom(ckptLSN, func(rec *aries.Record) bool {
		if rec.Type == aries.TypeCLR {
			fmt.Printf("  LSN=%-4d CLR T%d 恢复 P%d[%d] <- %q undoNext=%d\n",
				rec.LSN, rec.TxnID, rec.PageID, rec.Offset, rec.NewData, rec.UndoNextLSN)
		}
		if rec.Type == aries.TypeEnd && rec.LSN > ckptLSN && rec.TxnID != 2 {
			fmt.Printf("  LSN=%-4d END T%d（事务回滚完成）\n", rec.LSN, rec.TxnID)
		}
		return true
	})
	must(r.Close())

	fmt.Println("========== 7. 恢复后磁盘上的最终状态 ==========")
	final, err := aries.OpenPageStore(dbPath)
	must(err)
	for _, p := range []uint32{1, 2, 3} {
		fmt.Printf("  P%d [0:2]=%q [8:10]=%q [16:18]=%q\n",
			p, read(final, p, 0), read(final, p, 8), read(final, p, 16))
	}
	fmt.Println("  已提交: T2->P2=\"B1\", T4->P3=\"D1\" 持久化成功")
	fmt.Println("  未提交: T1/T3/T5 的全部修改已回滚（恢复为零值）")
}

func read(ps *aries.PageStore, page uint32, off uint32) string {
	b, _ := ps.ReadAt(page, off, 2)
	return string(b)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
