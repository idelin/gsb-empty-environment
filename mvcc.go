// Package mvcc 实现单进程内存版 MVCC（多版本并发控制）存储引擎。
//
// 核心语义：
//   - 每个事务分配单调自增的事务 ID；
//   - 读操作基于快照可见性：只能看到事务开始时已提交的最新版本（以及自己的写入）；
//   - 写写冲突采用首写获胜（First-Committer-Wins），冲突事务被中止（Abort）。
package mvcc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrTxnAborted 表示事务已被中止（如写写冲突），其后续操作均失败。
var ErrTxnAborted = errors.New("mvcc: transaction aborted")

// ErrTxnCommitted 表示事务已提交，不能再执行任何操作。
var ErrTxnCommitted = errors.New("mvcc: transaction already committed")

// ErrKeyNotFound 表示在事务的快照中该键不存在（无可见版本）。
var ErrKeyNotFound = errors.New("mvcc: key not found")

// version 是某个键的一个已提交版本。
type version struct {
	value     []byte // nil 表示 tombstone（删除）
	commitSeq uint64 // 提交序号（全局单调递增，在提交时分配）
}

// chain 是单个键的版本链，按 commitSeq 升序追加。
type chain struct {
	versions []version
}

// latestVisible 返回 commitSeq <= snapshotSeq 的最新版本。
func (c *chain) latestVisible(snapshotSeq uint64) (version, bool) {
	for i := len(c.versions) - 1; i >= 0; i-- {
		if v := c.versions[i]; v.commitSeq <= snapshotSeq {
			return v, true
		}
	}
	return version{}, false
}

// Engine 是 MVCC 存储引擎，可安全地用于并发事务。
type Engine struct {
	mu        sync.Mutex
	nextTxnID uint64            // 下一个待分配的事务 ID（单调自增）
	commitSeq uint64            // 全局提交序号（单调递增，每次成功提交 +1）
	data      map[string]*chain // key -> 已提交版本链
	pending   map[string]uint64 // key -> 持有该键未提交写入的事务 ID
}

// NewEngine 创建一个空的 MVCC 引擎，事务 ID 从 1 开始单调自增。
func NewEngine() *Engine {
	return &Engine{
		nextTxnID: 1,
		data:      make(map[string]*chain),
		pending:   make(map[string]uint64),
	}
}

// Txn 表示一个事务。单个事务不是并发安全的，应由单个 goroutine 使用。
type Txn struct {
	engine      *Engine
	id          uint64            // 事务 ID（单调自增）
	snapshotSeq uint64            // 快照序号：仅可见 commitSeq <= snapshotSeq 的版本
	writes      map[string][]byte // 本事务的写集（未提交，nil 值为 tombstone）
	aborted     bool
	committed   bool
}

// Begin 开启一个新事务：分配单调自增的事务 ID，并固定其读快照
// （快照 = 开启时刻的全局提交序号，之后其他事务的提交对本事务不可见）。
func (e *Engine) Begin() *Txn {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.nextTxnID
	e.nextTxnID++
	return &Txn{
		engine:      e,
		id:          id,
		snapshotSeq: e.commitSeq,
		writes:      make(map[string][]byte),
	}
}

// ID 返回事务 ID。
func (t *Txn) ID() uint64 { return t.id }

// Get 基于快照可见性读取 key：
// 优先返回本事务自己的未提交写入（read-your-writes），
// 否则返回事务开启时已提交的最新版本。
func (t *Txn) Get(key string) ([]byte, error) {
	if err := t.checkActive(); err != nil {
		return nil, err
	}
	if v, ok := t.writes[key]; ok {
		if v == nil {
			return nil, ErrKeyNotFound // 本事务已删除该键
		}
		return clone(v), nil
	}
	t.engine.mu.Lock()
	defer t.engine.mu.Unlock()
	c, ok := t.engine.data[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	v, ok := c.latestVisible(t.snapshotSeq)
	if !ok || v.value == nil {
		return nil, ErrKeyNotFound
	}
	return clone(v.value), nil
}

// Put 写入 key。若其他事务持有该键的未提交写入，则触发写写冲突，
// 本事务被中止（首写获胜：先写入者保留，后写入者失败）。
func (t *Txn) Put(key string, value []byte) error {
	if err := t.checkActive(); err != nil {
		return err
	}
	t.engine.mu.Lock()
	defer t.engine.mu.Unlock()
	if holder, ok := t.engine.pending[key]; ok && holder != t.id {
		t.abortLocked()
		return fmt.Errorf("%w: write-write conflict on key %q with txn %d", ErrTxnAborted, key, holder)
	}
	t.writes[key] = clone(value)
	t.engine.pending[key] = t.id
	return nil
}

// Delete 删除 key（写入 tombstone），冲突检测语义与 Put 一致。
func (t *Txn) Delete(key string) error {
	return t.Put(key, nil)
}

// Commit 提交事务。提交前做首写获胜校验：
// 若写集中某个键在本事务开始后被其他事务提交了新版本，则本事务中止。
// 提交成功后，写集作为新版本对其他事务可见。
func (t *Txn) Commit() error {
	if err := t.checkActive(); err != nil {
		return err
	}
	t.engine.mu.Lock()
	defer t.engine.mu.Unlock()

	// First-Committer-Wins：写集中的键若在本事务快照之后被其他事务提交，则本事务失败。
	for key := range t.writes {
		if c, ok := t.engine.data[key]; ok {
			if v, found := c.latestVisible(t.engine.commitSeq); found && v.commitSeq > t.snapshotSeq {
				t.abortLocked()
				return fmt.Errorf("%w: write-write conflict on key %q (first-committer-wins)", ErrTxnAborted, key)
			}
		}
	}

	// 分配提交序号，把写集追加为新的已提交版本（保持版本链按 commitSeq 升序）。
	t.engine.commitSeq++
	seq := t.engine.commitSeq
	keys := make([]string, 0, len(t.writes))
	for key := range t.writes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		c := t.engine.data[key]
		if c == nil {
			c = &chain{}
			t.engine.data[key] = c
		}
		c.versions = append(c.versions, version{value: clone(t.writes[key]), commitSeq: seq})
		delete(t.engine.pending, key)
	}
	t.committed = true
	return nil
}

// Abort 主动中止事务，释放其持有的未提交写入。
func (t *Txn) Abort() error {
	if t.committed {
		return ErrTxnCommitted
	}
	if t.aborted {
		return ErrTxnAborted
	}
	t.engine.mu.Lock()
	defer t.engine.mu.Unlock()
	t.abortLocked()
	return nil
}

// abortLocked 在持有 engine.mu 时中止事务并清理 pending 记录。
func (t *Txn) abortLocked() {
	for key := range t.writes {
		if holder, ok := t.engine.pending[key]; ok && holder == t.id {
			delete(t.engine.pending, key)
		}
	}
	t.writes = nil
	t.aborted = true
}

// checkActive 校验事务仍处于活跃状态。
func (t *Txn) checkActive() error {
	if t.aborted {
		return ErrTxnAborted
	}
	if t.committed {
		return ErrTxnCommitted
	}
	return nil
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
