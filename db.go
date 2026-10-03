// Package ssi 实现可串行化快照隔离（Serializable Snapshot Isolation）。
//
// 基础是 MVCC 快照隔离（SI）：每个事务在一致的快照上读取，写写在提交时
// 按 first-committer-wins 检测。在此之上，通过 SIREAD 锁追踪
// rw-antidependency（读写反依赖）：事务 R 读到的版本被并发事务 W 覆写，
// 形成 R -rw-> W。当冲突图中出现危险结构（dangerous structure）
// T_in -rw-> T_pivot -rw-> T_out 时，说明存在写倾斜（Write Skew）异常的
// 前夕，立即中止环上某个活跃事务，从而保证完全可串行化。
package ssi

import (
	"errors"
	"sync"
)

var (
	// ErrSerialization 表示检测到 rw-反依赖危险结构，事务被 SSI 中止。
	ErrSerialization = errors.New("ssi: serialization failure (rw-antidependency dangerous structure)")
	// ErrWriteConflict 表示写写冲突（first-committer-wins）。
	ErrWriteConflict = errors.New("ssi: write-write conflict")
	// ErrAborted 表示事务已被中止。
	ErrAborted = errors.New("ssi: transaction aborted")
	// ErrCommitted 表示事务已提交。
	ErrCommitted = errors.New("ssi: transaction already committed")
)

const maxTS = int64(^uint64(0) >> 1)

// version 是某个 key 的一个已提交版本。siread 记录持有该版本
// SIREAD 锁的事务 ID（读即加锁，不阻塞写，仅用于冲突检测）。
type version struct {
	value    int64
	beginTS  int64 // 写入者的提交时间戳
	endTS    int64 // 被更新版本取代的提交时间戳；maxTS 表示当前版本
	writerID int64
	siread   map[int64]bool
}

// record 是单个 key 的版本链，新版本在切片头部。
type record struct {
	versions []*version
}

func (r *record) visibleAt(ts int64) *version {
	for _, v := range r.versions {
		if v.beginTS <= ts {
			return v
		}
	}
	return nil
}

type txnState int

const (
	stateActive txnState = iota
	stateCommitted
	stateAborted
)

// DB 是支持 SSI 的内存 KV 库。
type DB struct {
	// DisableSSI 为 true 时退化为纯快照隔离（用于对照演示写倾斜异常）。
	DisableSSI bool

	mu      sync.Mutex
	nextTS  int64
	nextTxn int64
	data    map[string]*record
	txns    map[int64]*Txn
}

// New 创建空数据库。
func New() *DB {
	return &DB{
		data: make(map[string]*record),
		txns: make(map[int64]*Txn),
	}
}

// Put 直接写入初始数据（系统事务，beginTS=0，对所有快照可见）。
func (db *DB) Put(key string, value int64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.data[key] = &record{versions: []*version{{
		value:    value,
		beginTS:  0,
		endTS:    maxTS,
		siread:   make(map[int64]bool),
	}}}
}

// Get 读取最新已提交值（管理接口，不属于任何事务）。
func (db *DB) Get(key string) int64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	if rec := db.data[key]; rec != nil {
		return rec.versions[0].value
	}
	return 0
}

// Txn 是一个快照隔离事务。
type Txn struct {
	db       *DB
	id       int64
	startTS  int64
	commitTS int64
	state    txnState

	writes   map[string]int64
	readVers []*version

	// 冲突图（仅 rw-antidependency）：
	// inConflict[x]  表示存在边 x -rw-> 本事务
	// outConflict[y] 表示存在边 本事务 -rw-> y
	inConflict  map[int64]bool
	outConflict map[int64]bool
}

// Begin 开启事务，快照为当前已提交状态。
func (db *DB) Begin() *Txn {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.nextTxn++
	t := &Txn{
		db:          db,
		id:          db.nextTxn,
		startTS:     db.nextTS,
		writes:      make(map[string]int64),
		inConflict:  make(map[int64]bool),
		outConflict: make(map[int64]bool),
	}
	db.txns[t.id] = t
	return t
}

func (t *Txn) checkActive() error {
	switch t.state {
	case stateAborted:
		return ErrAborted
	case stateCommitted:
		return ErrCommitted
	}
	return nil
}

// Get 在事务快照上读取 key。读操作会：
//  1. 对可见版本加 SIREAD 锁；
//  2. 若该版本已被快照之后的提交覆写，建立 rw-反依赖边 本事务 -rw-> 覆写者，
//     并做危险结构检测。
func (t *Txn) Get(key string) (int64, error) {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if err := t.checkActive(); err != nil {
		return 0, err
	}
	if v, ok := t.writes[key]; ok {
		return v, nil // 读自己的写
	}
	rec := t.db.data[key]
	if rec == nil {
		return 0, nil // 不处理幻影（phantom），超出本演示范围
	}
	ver := rec.visibleAt(t.startTS)
	if ver == nil {
		return 0, nil
	}
	// SIREAD 锁
	ver.siread[t.id] = true
	t.readVers = append(t.readVers, ver)
	// 若可见版本已被覆写，则覆写者在本事务快照之后提交：
	// 存在 rw-antidependency 本事务 -rw-> 覆写者。
	if ver.endTS != maxTS {
		for _, newer := range rec.versions {
			if newer.beginTS == ver.endTS {
				t.db.addConflictLocked(t.id, newer.writerID)
				break
			}
		}
	}
	return ver.value, nil
}

// Set 缓冲写入，并对当前版本上的 SIREAD 锁建立 rw-反依赖边
// 读者 -rw-> 本事务，做危险结构检测。
func (t *Txn) Set(key string, value int64) error {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if err := t.checkActive(); err != nil {
		return err
	}
	if rec := t.db.data[key]; rec != nil {
		latest := rec.versions[0]
		if latest.beginTS > t.startTS {
			t.abortLocked()
			return ErrWriteConflict
		}
		t.db.checkSireadLocked(latest, t)
		if t.state == stateAborted {
			return ErrSerialization
		}
	}
	t.writes[key] = value
	return nil
}

// Commit 提交事务。提交前重新做写写冲突与 SIREAD 检测
//（覆盖 Set 之后、Commit 之前新出现的读者/写者），然后应用写入。
func (t *Txn) Commit() error {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	switch t.state {
	case stateAborted:
		return ErrAborted
	case stateCommitted:
		return ErrCommitted
	}
	for key := range t.writes {
		rec := t.db.data[key]
		if rec == nil {
			continue
		}
		latest := rec.versions[0]
		if latest.beginTS > t.startTS {
			t.abortLocked()
			return ErrWriteConflict
		}
		t.db.checkSireadLocked(latest, t)
	}
	if t.state == stateAborted {
		return ErrSerialization
	}
	t.db.nextTS++
	t.commitTS = t.db.nextTS
	for key, value := range t.writes {
		rec := t.db.data[key]
		if rec == nil {
			rec = &record{}
			t.db.data[key] = rec
		}
		if len(rec.versions) > 0 {
			rec.versions[0].endTS = t.commitTS
		}
		rec.versions = append([]*version{{
			value:    value,
			beginTS:  t.commitTS,
			endTS:    maxTS,
			writerID: t.id,
			siread:   make(map[int64]bool),
		}}, rec.versions...)
	}
	t.state = stateCommitted
	return nil
}

// Abort 主动回滚事务。
func (t *Txn) Abort() {
	t.db.mu.Lock()
	defer t.db.mu.Unlock()
	if t.state == stateActive {
		t.abortLocked()
	}
}

// checkSireadLocked 对 ver 上的每个并发 SIREAD 读者 r 建立边 r -rw-> w。
// 并发的定义：r 仍活跃，或 r 的提交发生在 w 的快照之后。
func (db *DB) checkSireadLocked(ver *version, w *Txn) {
	for rid := range ver.siread {
		if rid == w.id {
			continue
		}
		r := db.txns[rid]
		if r == nil || r.state == stateAborted {
			continue
		}
		if r.state == stateCommitted && r.commitTS <= w.startTS {
			continue // 不并发
		}
		db.addConflictLocked(rid, w.id)
	}
}

// addConflictLocked 添加 rw-反依赖边 from -rw-> to 并检测危险结构。
func (db *DB) addConflictLocked(from, to int64) {
	if db.DisableSSI || from == to {
		return
	}
	f, tt := db.txns[from], db.txns[to]
	if f == nil || tt == nil || f.state == stateAborted || tt.state == stateAborted {
		return
	}
	if f.outConflict[to] {
		return
	}
	f.outConflict[to] = true
	tt.inConflict[from] = true
	db.checkDangerousLocked(from, to)
}

// checkDangerousLocked 在新增边 a -rw-> b 后，检查是否形成危险结构
//（长度为 2 的 rw-反依赖链，含 a==c 的二元环）。新边既可能作为链尾
//（a->b->c），也可能作为链头（x->a->b）。
func (db *DB) checkDangerousLocked(a, b int64) {
	for c := range db.txns[b].outConflict {
		db.dangerousLocked(a, b, c)
	}
	for x := range db.txns[a].inConflict {
		db.dangerousLocked(x, a, b)
	}
}

// dangerousLocked 处理危险结构 x -rw-> y -rw-> z：中止环上第一个仍活跃
// 的事务（优先枢纽 y，其次 x，再次 z），阻止写倾斜异常真正发生。
// 边的创建者必然活跃，因此总能找到可中止的受害者。
func (db *DB) dangerousLocked(x, y, z int64) {
	for _, id := range []int64{y, x, z} {
		if v := db.txns[id]; v != nil && v.state == stateActive {
			v.abortLocked()
			return
		}
	}
}

// abortLocked 中止事务：释放其 SIREAD 锁，并从冲突图中移除相关边。
func (t *Txn) abortLocked() {
	t.state = stateAborted
	for _, ver := range t.readVers {
		delete(ver.siread, t.id)
	}
	for id := range t.outConflict {
		if o := t.db.txns[id]; o != nil {
			delete(o.inConflict, t.id)
		}
	}
	for id := range t.inConflict {
		if o := t.db.txns[id]; o != nil {
			delete(o.outConflict, t.id)
		}
	}
	t.outConflict = make(map[int64]bool)
	t.inConflict = make(map[int64]bool)
}
