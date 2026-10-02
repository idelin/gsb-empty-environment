package aries

import (
	"container/heap"
	"io"
	"sort"
)

// RecoveryResult 汇总三阶段恢复的统计信息,供调用方与测试断言。
type RecoveryResult struct {
	CheckpointLSN    LSN                 // 使用的最近检查点;无则为 NilLSN
	RedoStartLSN     LSN                 // Redo 扫描起点(DPT 中最小 recLSN)
	DirtyPageTable   map[uint64]LSN      // Analysis 重建的 DPT:页 -> recLSN
	TransactionTable map[uint64]TxnState // Analysis 结束时的 ATT 终态
	RedoneCount      int                 // Redo 实际重放的记录数
	SkippedCount     int                 // Redo 跳过的记录数(页已更新)
	CLRCount         int                 // Undo 写入的 CLR 数
	UndoneTxns       []uint64            // 被回滚的败者事务(升序)
}

type attEntry struct {
	lastLSN LSN
	state   TxnState
}

// Recover 执行 ARIES 三阶段恢复:Analysis -> Redo -> Undo。
// 直接作用于磁盘管理器(恢复期间缓冲池为空),幂等,可重复执行。
func Recover(lm *LogManager, dm *DiskManager) (*RecoveryResult, error) {
	res := &RecoveryResult{
		DirtyPageTable:   make(map[uint64]LSN),
		TransactionTable: make(map[uint64]TxnState),
	}

	// ---------- 定位最近的检查点 ----------
	var ckpt *LogRecord
	it := lm.Iterator(0)
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if rec.Type == TypeCheckpoint && rec.Checkpoint != nil {
			ckpt = rec
		}
	}

	// ---------- 阶段 1:Analysis,重建 ATT 与 DPT ----------
	att := make(map[uint64]*attEntry)
	dpt := res.DirtyPageTable
	startScan := LSN(0)
	if ckpt != nil {
		res.CheckpointLSN = ckpt.LSN
		startScan = ckpt.LSN
		for id, last := range ckpt.Checkpoint.ActiveTxns {
			att[id] = &attEntry{lastLSN: last, state: TxnRunning}
		}
		for page, recLSN := range ckpt.Checkpoint.DirtyPages {
			dpt[page] = recLSN
		}
	}
	it = lm.Iterator(startScan)
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch rec.Type {
		case TypeBegin:
			att[rec.TxnID] = &attEntry{lastLSN: rec.LSN, state: TxnRunning}
		case TypeUpdate, TypeCLR:
			e, ok := att[rec.TxnID]
			if !ok {
				e = &attEntry{state: TxnRunning}
				att[rec.TxnID] = e
			}
			e.lastLSN = rec.LSN
			if _, ok := dpt[rec.PageID]; !ok {
				dpt[rec.PageID] = rec.LSN
			}
		case TypeCommit:
			if e, ok := att[rec.TxnID]; ok {
				e.lastLSN = rec.LSN
				e.state = TxnCommitted
			}
		case TypeAbort:
			if e, ok := att[rec.TxnID]; ok {
				e.lastLSN = rec.LSN
				e.state = TxnAborted
			}
		case TypeEnd:
			delete(att, rec.TxnID)
		}
	}
	for id, e := range att {
		res.TransactionTable[id] = e.state
	}

	// ---------- 阶段 2:Redo,历史重复 ----------
	redoStart := LSN(0)
	first := true
	for _, recLSN := range dpt {
		if first || recLSN < redoStart {
			redoStart = recLSN
			first = false
		}
	}
	res.RedoStartLSN = redoStart
	if len(dpt) > 0 {
		it = lm.Iterator(redoStart)
		for {
			rec, err := it.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if rec.Type != TypeUpdate && rec.Type != TypeCLR {
				continue
			}
			recLSN, ok := dpt[rec.PageID]
			if !ok || recLSN > rec.LSN {
				res.SkippedCount++
				continue
			}
			pageLSN, err := dm.PageLSN(rec.PageID)
			if err != nil {
				return nil, err
			}
			if pageLSN >= rec.LSN {
				res.SkippedCount++
				continue
			}
			data, _, err := dm.ReadPage(rec.PageID)
			if err != nil {
				return nil, err
			}
			copy(data[rec.Offset:], rec.AfterImage)
			if err := dm.WritePage(rec.PageID, data, rec.LSN); err != nil {
				return nil, err
			}
			res.RedoneCount++
		}
	}

	// ---------- 阶段 3:Undo,反向扫描败者事务 ----------
	// 已提交但未写 END 的事务补写 END。
	for id, e := range att {
		if e.state == TxnCommitted {
			if _, err := lm.Append(&LogRecord{Type: TypeEnd, TxnID: id, PrevLSN: e.lastLSN}); err != nil {
				return nil, err
			}
			delete(att, id)
		}
	}
	// 最大堆:每次取全局最大 LSN,保证严格逆序。
	h := &lsnHeap{}
	for _, e := range att {
		heap.Push(h, e.lastLSN)
	}
	endTxn := func(txnID uint64) error {
		e, ok := att[txnID]
		if !ok {
			return nil
		}
		if _, err := lm.Append(&LogRecord{Type: TypeEnd, TxnID: txnID, PrevLSN: e.lastLSN}); err != nil {
			return err
		}
		delete(att, txnID)
		res.UndoneTxns = append(res.UndoneTxns, txnID)
		res.TransactionTable[txnID] = TxnAborted
		return nil
	}
	for h.Len() > 0 {
		lsn := heap.Pop(h).(LSN)
		rec, err := lm.ReadRecord(lsn)
		if err != nil {
			return nil, err
		}
		switch rec.Type {
		case TypeUpdate:
			e, ok := att[rec.TxnID]
			if !ok {
				continue
			}
			clr := &LogRecord{
				Type:        TypeCLR,
				TxnID:       rec.TxnID,
				PrevLSN:     e.lastLSN,
				PageID:      rec.PageID,
				Offset:      rec.Offset,
				UndoNextLSN: rec.PrevLSN,
				AfterImage:  rec.BeforeImage,
			}
			clrLSN, err := lm.Append(clr)
			if err != nil {
				return nil, err
			}
			data, _, err := dm.ReadPage(rec.PageID)
			if err != nil {
				return nil, err
			}
			copy(data[rec.Offset:], rec.BeforeImage)
			if err := dm.WritePage(rec.PageID, data, clrLSN); err != nil {
				return nil, err
			}
			e.lastLSN = clrLSN
			res.CLRCount++
			if rec.PrevLSN != NilLSN {
				heap.Push(h, rec.PrevLSN)
			} else if err := endTxn(rec.TxnID); err != nil {
				return nil, err
			}
		case TypeCLR:
			if rec.UndoNextLSN != NilLSN {
				heap.Push(h, rec.UndoNextLSN)
			} else if err := endTxn(rec.TxnID); err != nil {
				return nil, err
			}
		case TypeBegin:
			if err := endTxn(rec.TxnID); err != nil {
				return nil, err
			}
		case TypeAbort:
			if rec.PrevLSN != NilLSN {
				heap.Push(h, rec.PrevLSN)
			} else if err := endTxn(rec.TxnID); err != nil {
				return nil, err
			}
		}
	}
	if err := lm.SyncAll(); err != nil {
		return nil, err
	}
	if err := dm.Sync(); err != nil {
		return nil, err
	}
	sort.Slice(res.UndoneTxns, func(i, j int) bool { return res.UndoneTxns[i] < res.UndoneTxns[j] })
	return res, nil
}

// RecoverDB 打开目录并执行恢复,返回可直接使用的 DB。
func RecoverDB(dir string, bufCapacity int) (*DB, *RecoveryResult, error) {
	db, err := OpenDB(dir, bufCapacity)
	if err != nil {
		return nil, nil, err
	}
	res, err := Recover(db.lm, db.dm)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, res, nil
}

// lsnHeap 是 LSN 最大堆。
type lsnHeap []LSN

func (h lsnHeap) Len() int            { return len(h) }
func (h lsnHeap) Less(i, j int) bool  { return h[i] > h[j] }
func (h lsnHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x interface{}) { *h = append(*h, x.(LSN)) }
func (h *lsnHeap) Pop() interface{} {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
