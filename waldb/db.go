package waldb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// DB 是有限页数据库：固定数量的页，每页一个 uint64 值，页头存 pageLSN。
type DB struct {
	logFile  *InjectedFile
	pageFile *InjectedFile
	numPages int
	lm       *LogManager
	bp       *BufferPool
	hook     func(CrashPoint)

	nextTxn int64
	tt      map[int64]*txnEntry // 活跃事务表（检查点快照用）

	onCommitDurable func(txnID int64) // commit 记录持久化后回调（测试取证用）
}

// NewDatabase 在空文件上创建数据库并写入初始页值。
func NewDatabase(logFile, pageFile *InjectedFile, numPages int, initial []uint64, hook func(CrashPoint)) *DB {
	if len(initial) != numPages {
		panic("initial values must match numPages")
	}
	for i := 0; i < numPages; i++ {
		buf := make([]byte, PageSize)
		binary.LittleEndian.PutUint64(buf[0:], ^uint64(0)) // pageLSN = NilLSN
		binary.LittleEndian.PutUint64(buf[8:], initial[i])
		pageFile.WriteAt(buf, int64(i*PageSize))
	}
	pageFile.Sync()
	logFile.Sync()
	return newDB(logFile, pageFile, numPages, hook)
}

// OpenDatabase 打开已有数据库并执行崩溃恢复（分析→重做→撤销）。
func OpenDatabase(logFile, pageFile *InjectedFile, numPages int, hook func(CrashPoint)) *DB {
	db := newDB(logFile, pageFile, numPages, hook)
	db.Recover()
	return db
}

func newDB(logFile, pageFile *InjectedFile, numPages int, hook func(CrashPoint)) *DB {
	lm := openLogManager(logFile, hook)
	db := &DB{
		logFile:  logFile,
		pageFile: pageFile,
		numPages: numPages,
		lm:       lm,
		hook:     hook,
		nextTxn:  1,
		tt:       make(map[int64]*txnEntry),
	}
	db.bp = newBufferPool(pageFile, numPages, lm, hook)
	return db
}

// SetOnCommitDurable 注册 commit 记录持久化后的回调。
func (db *DB) SetOnCommitDurable(fn func(txnID int64)) { db.onCommitDurable = fn }

// FlushPages 刷出全部脏页（steal）。
func (db *DB) FlushPages() { db.bp.FlushAll() }

// ReadPage 读页当前值（经缓冲池）。
func (db *DB) ReadPage(id int32) uint64 { return db.bp.fetch(id).value }

func (db *DB) fire(p CrashPoint) {
	if db.hook != nil {
		db.hook(p)
	}
}

// Txn 是一个采用严格两阶段独占页锁的事务。
type Txn struct {
	db      *DB
	id      int64
	prevLSN int64
	active  bool
}

// Begin 开启事务并写 begin 记录。
func (db *DB) Begin() *Txn {
	id := db.nextTxn
	db.nextTxn++
	lsn := db.lm.Append(&LogRecord{Type: recBegin, TxnID: id, PrevLSN: NilLSN})
	db.tt[id] = &txnEntry{Status: statusRunning, LastLSN: lsn}
	return &Txn{db: db, id: id, prevLSN: lsn, active: true}
}

// Update 以独占锁更新一页，先写 update 日志（含 before/after），再改页。
func (t *Txn) Update(pageID int32, value uint64) {
	if !t.active {
		panic("update on inactive transaction")
	}
	t.db.bp.locks.lock(t.id, pageID)
	p := t.db.bp.fetch(pageID)
	rec := &LogRecord{
		Type: recUpdate, TxnID: t.id, PrevLSN: t.prevLSN,
		PageID: pageID, Before: p.value, After: value,
	}
	lsn := t.db.lm.Append(rec)
	t.prevLSN = lsn
	if e := t.db.tt[t.id]; e != nil {
		e.LastLSN = lsn
	}
	t.db.bp.apply(p, value, lsn)
}

// Commit 提交事务：commit 记录持久化后才算提交成功，随后写 END 并放锁。
func (t *Txn) Commit() {
	if !t.active {
		panic("commit on inactive transaction")
	}
	lsn := t.db.lm.Append(&LogRecord{Type: recCommit, TxnID: t.id, PrevLSN: t.prevLSN})
	t.db.lm.Flush(lsn) // 提交成功的持久化点
	if e := t.db.tt[t.id]; e != nil {
		e.Status = statusCommitted
		e.LastLSN = lsn
	}
	if t.db.onCommitDurable != nil {
		t.db.onCommitDurable(t.id)
	}
	t.db.fire(CutCommitDurable)
	t.db.fire(CutBeforeEnd)
	t.db.lm.Append(&LogRecord{Type: recEnd, TxnID: t.id, PrevLSN: lsn})
	delete(t.db.tt, t.id)
	t.db.bp.locks.releaseAll(t.id)
	t.active = false
}

// Abort 回滚事务：写 abort 记录，沿 prevLSN 链逐条写 CLR 撤销，最后写 END。
func (t *Txn) Abort() {
	if !t.active {
		panic("abort on inactive transaction")
	}
	lsn := t.db.lm.Append(&LogRecord{Type: recAbort, TxnID: t.id, PrevLSN: t.prevLSN})
	t.db.lm.Flush(lsn)
	if e := t.db.tt[t.id]; e != nil {
		e.Status = statusAborting
		e.LastLSN = lsn
	}
	next := lsn
	for next != NilLSN {
		rec := t.db.lm.ReadRecord(next)
		next = t.db.undoStep(t.db.tt, t.id, rec)
	}
	delete(t.db.tt, t.id)
	t.db.bp.locks.releaseAll(t.id)
	t.active = false
}

// Checkpoint 模糊检查点：先持久化 begin_checkpoint，再收集事务表与脏页表
// 快照写入 end_checkpoint。允许只完成一部分——崩溃导致 end_checkpoint
// 缺失或不完整时，分析阶段回退到上一个完整检查点或日志起点。
func (db *DB) Checkpoint() {
	begin := &LogRecord{Type: recCheckpointBegin, TxnID: 0, PrevLSN: NilLSN}
	lsn := db.lm.Append(begin)
	db.lm.Flush(lsn)
	db.fire(CutCheckpointPartial) // 部分检查点：起点已持久，终点未写

	snap := checkpointSnapshot{TT: make(map[int64]txnEntry), DPT: db.bp.dirtyPageTable()}
	for id, e := range db.tt {
		snap.TT[id] = *e
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		panic(err)
	}
	end := &LogRecord{Type: recCheckpointEnd, TxnID: 0, PrevLSN: NilLSN, Payload: payload}
	db.lm.Append(end)
	db.lm.Flush(db.lm.endLSN())
}

// String 便于调试。
func (db *DB) String() string {
	return fmt.Sprintf("DB{pages:%d, logEnd:%d}", db.numPages, db.lm.endLSN())
}
