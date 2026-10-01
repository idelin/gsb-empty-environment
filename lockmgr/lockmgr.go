// Package lockmgr 实现行级悲观锁管理器，支持共享锁(S)/排他锁(X)、
// 超时等待，以及 Wait-Die / Wound-Wait 两种死锁预防策略。
//
// 事务按开始顺序分配单调递增的时间戳 ID（越小越老）。当锁请求与
// 已持有锁冲突时，根据策略比较时间戳决定“等待”或“自杀/抢占”，
// 从而保证系统全局无死锁。
package lockmgr

import (
	"errors"
	"sync"
	"time"
)

// TxnID 是事务时间戳，越小表示事务越老。
type TxnID uint64

// LockMode 锁模式：共享锁(S) 或 排他锁(X)。
type LockMode int

const (
	Shared LockMode = iota
	Exclusive
)

func (m LockMode) String() string {
	if m == Shared {
		return "S"
	}
	return "X"
}

// Policy 死锁预防策略。
type Policy int

const (
	// WaitDie 非抢占式：老事务等待年轻事务；年轻事务遇到老事务持锁时自杀(中止)。
	WaitDie Policy = iota
	// WoundWait 抢占式：老事务抢占(Wound)年轻事务迫使其中止重启；年轻事务等待老事务。
	WoundWait
)

func (p Policy) String() string {
	if p == WaitDie {
		return "Wait-Die"
	}
	return "Wound-Wait"
}

var (
	// ErrAborted 事务被中止（Wait-Die 自杀或 Wound-Wait 被抢占）。
	ErrAborted = errors.New("lockmgr: transaction aborted")
	// ErrTimeout 锁请求超时。
	ErrTimeout = errors.New("lockmgr: lock request timed out")
	// ErrFinished 事务已提交或回滚。
	ErrFinished = errors.New("lockmgr: transaction already finished")
)

// LockManager 行级锁管理器。
type LockManager struct {
	mu      sync.Mutex
	policy  Policy
	timeout time.Duration
	nextID  TxnID
	rows    map[string]*rowState
	txns    map[TxnID]*Txn
	// sleepCh 是全局广播通道：任何锁状态变化都会 close 它并重建，
	// 所有等待者被唤醒后重新检查锁是否可授予。
	sleepCh chan struct{}
}

// NewLockManager 创建锁管理器。timeout 为单次锁请求的最大等待时间。
func NewLockManager(policy Policy, timeout time.Duration) *LockManager {
	return &LockManager{
		policy:  policy,
		timeout: timeout,
		rows:    make(map[string]*rowState),
		txns:    make(map[TxnID]*Txn),
		sleepCh: make(chan struct{}),
	}
}

// Policy 返回当前死锁预防策略。
func (lm *LockManager) Policy() Policy { return lm.policy }

type rowState struct {
	holders map[TxnID]LockMode
	waiters map[TxnID]struct{}
}

func (lm *LockManager) row(row string) *rowState {
	rs, ok := lm.rows[row]
	if !ok {
		rs = &rowState{
			holders: make(map[TxnID]LockMode),
			waiters: make(map[TxnID]struct{}),
		}
		lm.rows[row] = rs
	}
	return rs
}

// broadcastLocked 唤醒所有等待者。调用时必须持有 lm.mu。
func (lm *LockManager) broadcastLocked() {
	close(lm.sleepCh)
	lm.sleepCh = make(chan struct{})
}

// Txn 事务句柄。
type Txn struct {
	mgr *LockManager
	// ID 即事务时间戳，越小越老。
	ID TxnID

	aborted  bool
	finished bool
	held     map[string]LockMode
}

// Begin 开启事务，分配递增时间戳 ID。
func (lm *LockManager) Begin() *Txn {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.nextID++
	txn := &Txn{mgr: lm, ID: lm.nextID, held: make(map[string]LockMode)}
	lm.txns[txn.ID] = txn
	return txn
}

// Aborted 报告事务是否已被中止（自杀或被抢占）。
func (t *Txn) Aborted() bool {
	lm := t.mgr
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return t.aborted
}

// checkActiveLocked 检查事务是否仍可请求锁。调用时必须持有 lm.mu。
func (t *Txn) checkActiveLocked() error {
	if t.aborted {
		return ErrAborted
	}
	if t.finished {
		return ErrFinished
	}
	return nil
}

// conflictingHolders 返回与 (self 请求 mode) 冲突的持有者 ID 列表。
// 兼容矩阵：S+S 兼容，其余组合均冲突。
func conflictingHolders(rs *rowState, self TxnID, mode LockMode) []TxnID {
	var out []TxnID
	for id, m := range rs.holders {
		if id == self {
			continue
		}
		if m == Exclusive || mode == Exclusive {
			out = append(out, id)
		}
	}
	return out
}

// Lock 请求行的 S/X 锁，带超时与死锁预防。
//
// 冲突时按策略处理：
//   - Wait-Die：请求者比所有冲突持有者都老则等待，否则自杀返回 ErrAborted；
//   - Wound-Wait：请求者比冲突持有者老则抢占（Wound）对方并继续，
//     否则等待。
//
// 等待超过管理器超时时间返回 ErrTimeout。
func (t *Txn) Lock(row string, mode LockMode) error {
	lm := t.mgr
	deadline := time.Now().Add(lm.timeout)

	for {
		lm.mu.Lock()
		if err := t.checkActiveLocked(); err != nil {
			lm.mu.Unlock()
			return err
		}
		rs := lm.row(row)
		conflicts := conflictingHolders(rs, t.ID, mode)

		if len(conflicts) == 0 {
			// 无冲突，授予锁（已有 X 锁时保持 X，否则取请求模式）。
			newMode := mode
			if t.held[row] == Exclusive {
				newMode = Exclusive
			}
			t.held[row] = newMode
			rs.holders[t.ID] = newMode
			delete(rs.waiters, t.ID)
			lm.mu.Unlock()
			return nil
		}

		switch lm.policy {
		case WaitDie:
			// 仅当比所有冲突持有者都老时才允许等待，否则自杀。
			olderThanAll := true
			for _, h := range conflicts {
				if h < t.ID {
					olderThanAll = false
					break
				}
			}
			if !olderThanAll {
				t.abortLocked()
				lm.mu.Unlock()
				return ErrAborted
			}
		case WoundWait:
			// 抢占所有比请求者年轻的冲突持有者。
			wounded := false
			for _, h := range conflicts {
				if h > t.ID {
					if victim, ok := lm.txns[h]; ok {
						victim.abortLocked()
						wounded = true
					}
				}
			}
			if wounded {
				// 被抢占者已释放锁，重新评估。
				lm.mu.Unlock()
				continue
			}
			// 所有冲突持有者都比请求者老，只能等待。
		}

		// 进入等待队列。
		rs.waiters[t.ID] = struct{}{}
		ch := lm.sleepCh
		remain := time.Until(deadline)
		lm.mu.Unlock()

		if remain <= 0 {
			lm.removeWaiter(row, t.ID)
			return ErrTimeout
		}
		timer := time.NewTimer(remain)
		select {
		case <-ch:
			timer.Stop()
		case <-timer.C:
			lm.removeWaiter(row, t.ID)
			return ErrTimeout
		}
	}
}

func (lm *LockManager) removeWaiter(row string, id TxnID) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	delete(lm.row(row).waiters, id)
}

// abortLocked 中止事务：标记 aborted、释放全部锁、移出等待队列并广播。
// 调用时必须持有 lm.mu。
func (t *Txn) abortLocked() {
	t.aborted = true
	t.mgr.releaseLocked(t)
}

// releaseLocked 释放事务持有的全部锁并广播唤醒等待者。调用时必须持有 lm.mu。
func (lm *LockManager) releaseLocked(t *Txn) {
	for row := range t.held {
		if rs, ok := lm.rows[row]; ok {
			delete(rs.holders, t.ID)
		}
	}
	t.held = make(map[string]LockMode)
	for _, rs := range lm.rows {
		delete(rs.waiters, t.ID)
	}
	lm.broadcastLocked()
}

// Commit 提交事务并释放全部锁。
func (t *Txn) Commit() { t.finish() }

// Rollback 回滚事务并释放全部锁。
func (t *Txn) Rollback() { t.finish() }

func (t *Txn) finish() {
	lm := t.mgr
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if t.finished {
		return
	}
	t.finished = true
	lm.releaseLocked(t)
	delete(lm.txns, t.ID)
}
