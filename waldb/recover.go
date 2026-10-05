package waldb

import "encoding/json"

type txnStatus int

const (
	statusRunning   txnStatus = iota // 运行中（崩溃时未提交）
	statusCommitted                  // commit 记录已持久化
	statusAborting                   // 回滚中
)

type txnEntry struct {
	Status  txnStatus
	LastLSN int64
}

// checkpointSnapshot 是 end_checkpoint 记录携带的事务表+脏页表快照。
type checkpointSnapshot struct {
	TT  map[int64]txnEntry `json:"tt"`
	DPT map[int32]int64    `json:"dpt"`
}

// Recover 执行 ARIES 式三阶段恢复。可重入：恢复过程中任意点崩溃后
// 再次恢复，结果一致（CLR 只重做、不重复撤销）。
func (db *DB) Recover() {
	tt, dpt := db.analyze()
	db.fire(CutRecoveryAnalysis)
	db.redo(dpt)
	db.fire(CutRecoveryRedo)
	db.undoLosers(tt)
	db.lm.Flush(db.lm.endLSN())
	db.bp.FlushAll()
	db.fire(CutRecoveryEnd)
}

// analyze 正向扫描日志，重建事务表与脏页表。
// 遇到完整的 end_checkpoint 时用其快照替换当前表（快照反映该记录时刻
// 的状态）；不完整尾记录由扫描器截断，天然回退到更早的可用恢复起点。
func (db *DB) analyze() (map[int64]*txnEntry, map[int32]int64) {
	tt := make(map[int64]*txnEntry)
	dpt := make(map[int32]int64)
	off := int64(0)
	for off < db.lm.endLSN() {
		rec, next, err := decodeAt(db.lm.log, off)
		if err != nil {
			break
		}
		off = next
		switch rec.Type {
		case recBegin:
			tt[rec.TxnID] = &txnEntry{Status: statusRunning, LastLSN: rec.LSN}
		case recUpdate, recCLR:
			e := tt[rec.TxnID]
			if e == nil {
				e = &txnEntry{Status: statusRunning}
				tt[rec.TxnID] = e
			}
			e.LastLSN = rec.LSN
			if _, ok := dpt[rec.PageID]; !ok {
				dpt[rec.PageID] = rec.LSN
			}
		case recCommit:
			if e := tt[rec.TxnID]; e != nil {
				e.Status = statusCommitted
				e.LastLSN = rec.LSN
			}
		case recAbort:
			if e := tt[rec.TxnID]; e != nil {
				e.Status = statusAborting
				e.LastLSN = rec.LSN
			}
		case recEnd:
			delete(tt, rec.TxnID)
		case recCheckpointEnd:
			var snap checkpointSnapshot
			if err := json.Unmarshal(rec.Payload, &snap); err == nil {
				tt = make(map[int64]*txnEntry, len(snap.TT))
				for id, e := range snap.TT {
					entry := e
					tt[id] = &entry
				}
				dpt = snap.DPT
				if dpt == nil {
					dpt = make(map[int32]int64)
				}
			}
		}
	}
	return tt, dpt
}

// redo 从脏页表最小 recLSN 开始正向重做历史：对 update 与 CLR，
// 只要页在脏页表中且 pageLSN < 记录 LSN，就应用 after 镜像。
// 幂等：pageLSN 保证同一记录不会重复应用。
func (db *DB) redo(dpt map[int32]int64) {
	start := db.lm.endLSN()
	for _, lsn := range dpt {
		if lsn < start {
			start = lsn
		}
	}
	off := start
	for off < db.lm.endLSN() {
		rec, next, err := decodeAt(db.lm.log, off)
		if err != nil {
			break
		}
		off = next
		if rec.Type != recUpdate && rec.Type != recCLR {
			continue
		}
		recLSN, ok := dpt[rec.PageID]
		if !ok || recLSN > rec.LSN {
			continue
		}
		p := db.bp.fetch(rec.PageID)
		if p.pageLSN >= rec.LSN {
			continue
		}
		db.bp.apply(p, rec.After, rec.LSN)
	}
}

// undoLosers 反向撤销所有未提交事务（running/aborting），
// 按 LSN 从大到小全局推进；随后为缺失 END 的已提交事务补写 END。
func (db *DB) undoLosers(tt map[int64]*txnEntry) {
	for {
		pick := int64(-1)
		var maxLSN int64 = NilLSN
		for id, e := range tt {
			if e.Status == statusCommitted {
				continue
			}
			if pick == -1 || e.LastLSN > maxLSN {
				pick = id
				maxLSN = e.LastLSN
			}
		}
		if pick == -1 {
			break
		}
		rec := db.lm.ReadRecord(maxLSN)
		next := db.undoStep(tt, pick, rec)
		if next == NilLSN {
			delete(tt, pick)
		} else {
			tt[pick].LastLSN = next
		}
	}
	for id, e := range tt {
		if e.Status == statusCommitted {
			db.lm.Append(&LogRecord{Type: recEnd, TxnID: id, PrevLSN: e.LastLSN})
			delete(tt, id)
		}
	}
}

// undoStep 处理撤销链上的一条记录，返回下一个待处理 LSN（NilLSN 表示链完）。
// 撤销 update 时先写 CLR（含 undoNextLSN）并持久化，再改页；
// CLR 本身只重做、永不撤销——重启后沿 undoNextLSN 跳过已撤销的记录。
func (db *DB) undoStep(tt map[int64]*txnEntry, txnID int64, rec *LogRecord) int64 {
	switch rec.Type {
	case recUpdate:
		clr := &LogRecord{
			Type: recCLR, TxnID: txnID, PrevLSN: rec.LSN,
			PageID: rec.PageID, Before: rec.After, After: rec.Before,
			UndoNextLSN: rec.PrevLSN,
		}
		clrLSN := db.lm.Append(clr)
		db.lm.Flush(clrLSN) // CLR 先持久化，再改页
		db.fire(CutRecoveryCLR)
		p := db.bp.fetch(rec.PageID)
		db.bp.apply(p, rec.Before, clrLSN)
		db.fire(CutRecoveryUndoPage)
		if e := tt[txnID]; e != nil {
			e.LastLSN = clrLSN
		}
		return rec.PrevLSN
	case recCLR:
		return rec.UndoNextLSN
	case recAbort, recCommit:
		return rec.PrevLSN
	case recBegin:
		db.lm.Append(&LogRecord{Type: recEnd, TxnID: txnID, PrevLSN: rec.LSN})
		return NilLSN
	}
	return NilLSN
}
