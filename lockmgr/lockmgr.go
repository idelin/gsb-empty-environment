// Package lockmgr 实现行级悲观锁管理器，支持共享锁(S)/排他锁(X)、
// 锁超时，以及 Wait-Die / Wound-Wait 两种基于事务时间戳的死锁预防策略，
// 保证系统全局无死锁运行。
//
// 约定：txnID 即事务时间戳，数值越小事务越老。
package lockmgr

import (
	"errors"
	"sync"
	"time"
)

// LockMode 锁模式
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

// Policy 死锁预防策略
type Policy int

const (
	// WaitDie 老事务等待年轻事务；年轻事务请求老事务持有的锁时自杀(Die)
	WaitDie Policy = iota
	// WoundWait 老事务抢占(Wound)年轻事务迫使其重启；年轻事务等待老事务
	WoundWait
)

func (p Policy) String() string {
	if p == WaitDie {
		return "Wait-Die"
	}
	return "Wound-Wait"
}

var (
	// ErrAborted 事务被死锁预防机制选中中止（Die 或被 Wound），需要重启
	ErrAborted = errors.New("lockmgr: transaction aborted by deadlock prevention, restart required")
	// ErrTimeout 锁等待超时
	ErrTimeout = errors.New("lockmgr: lock request timed out")
)

// request 队列中的等待请求
type request struct {
	txnID uint64
	mode  LockMode
	ch    chan error // 缓冲为1，授权/中止时通知
}

// lockEntry 单行上的锁状态
type lockEntry struct {
	holders map[uint64]LockMode
	queue   []*request
}

// Manager 行级锁管理器
type Manager struct {
	mu      sync.Mutex
	policy  Policy
	entries map[string]*lockEntry
	heldBy  map[uint64]map[string]LockMode // txn -> 持有的行锁
	aborted map[uint64]bool                // 被死锁预防机制中止的事务
}

// NewManager 创建指定死锁预防策略的锁管理器
func NewManager(policy Policy) *Manager {
	return &Manager{
		policy:  policy,
		entries: make(map[string]*lockEntry),
		heldBy:  make(map[uint64]map[string]LockMode),
		aborted: make(map[uint64]bool),
	}
}

// Lock 为事务 txnID 请求行 row 上的 mode 锁。
// timeout <= 0 表示无限等待。返回 nil 表示加锁成功；
// 返回 ErrAborted 表示本事务被死锁预防机制中止，应回滚并重启；
// 返回 ErrTimeout 表示等待超时。
func (m *Manager) Lock(txnID uint64, row string, mode LockMode, timeout time.Duration) error {
	m.mu.Lock()
	if m.aborted[txnID] {
		m.mu.Unlock()
		return ErrAborted
	}
	e := m.entry(row)

	// 已持有同级或更强锁，直接返回
	if held, ok := e.holders[txnID]; ok {
		if held == Exclusive || held == mode {
			m.mu.Unlock()
			return nil
		}
		// S -> X 升级：自己是唯一持有者时直接升级
		if len(e.holders) == 1 {
			e.holders[txnID] = Exclusive
			m.heldBy[txnID][row] = Exclusive
			m.mu.Unlock()
			return nil
		}
	}

	// 无冲突且无人排队，直接授权（保证FIFO公平）
	if len(e.queue) == 0 && m.canGrant(e, txnID, mode) {
		m.grant(e, row, txnID, mode)
		m.mu.Unlock()
		return nil
	}

	// 存在冲突持有者，按策略决定 等待/自杀/抢占
	conflicting := conflictingHolders(e, txnID, mode)
	if len(conflicting) > 0 {
		switch m.policy {
		case WaitDie:
			// 只要有冲突持有者比请求者老，年轻的请求者自杀(Die)；否则等待
			for _, h := range conflicting {
				if h < txnID {
					m.abortLocked(txnID)
					m.mu.Unlock()
					return ErrAborted
				}
			}
		case WoundWait:
			// 请求者比所有冲突持有者都老：抢占(Wound)它们
			older := true
			for _, h := range conflicting {
				if h < txnID {
					older = false
					break
				}
			}
			if older {
				// 先将被抢占者从本行移除（暂不唤醒本行队列），
				// 保证抢占者优先获得锁
				for _, h := range conflicting {
					m.aborted[h] = true
					delete(e.holders, h)
					if rows, ok := m.heldBy[h]; ok {
						delete(rows, row)
						if len(rows) == 0 {
							delete(m.heldBy, h)
						}
					}
					m.releaseAllLocked(h)
					m.rejectWaitingLocked(h)
				}
				if m.canGrant(e, txnID, mode) {
					m.grant(e, row, txnID, mode)
					m.processQueue(row, e)
					m.mu.Unlock()
					return nil
				}
			}
		}
	}

	// 进入等待队列
	req := &request{txnID: txnID, mode: mode, ch: make(chan error, 1)}
	e.queue = append(e.queue, req)
	m.mu.Unlock()

	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case err := <-req.ch:
		return err
	case <-timer:
		m.mu.Lock()
		m.removeRequest(e, req)
		m.mu.Unlock()
		return ErrTimeout
	}
}

// Unlock 释放事务在指定行上的锁
func (m *Manager) Unlock(txnID uint64, row string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[row]
	if !ok {
		return
	}
	if _, ok := e.holders[txnID]; !ok {
		return
	}
	delete(e.holders, txnID)
	if rows, ok := m.heldBy[txnID]; ok {
		delete(rows, row)
		if len(rows) == 0 {
			delete(m.heldBy, txnID)
		}
	}
	m.processQueue(row, e)
}

// Commit 提交事务，释放其持有的全部行锁
func (m *Manager) Commit(txnID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseAllLocked(txnID)
}

// Abort 主动回滚事务，释放其持有的全部行锁
func (m *Manager) Abort(txnID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseAllLocked(txnID)
	delete(m.aborted, txnID)
}

// Restart 重启一个被死锁预防机制中止的事务（清除中止标记后重新执行）
func (m *Manager) Restart(txnID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseAllLocked(txnID)
	delete(m.aborted, txnID)
}

// IsAborted 查询事务是否已被死锁预防机制中止
func (m *Manager) IsAborted(txnID uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.aborted[txnID]
}

// Holds 查询事务是否持有指定行的锁（用于测试与演示）
func (m *Manager) Holds(txnID uint64, row string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[row]
	if !ok {
		return false
	}
	_, ok = e.holders[txnID]
	return ok
}

// ---- 内部方法（调用时必须持有 m.mu）----

func (m *Manager) entry(row string) *lockEntry {
	e, ok := m.entries[row]
	if !ok {
		e = &lockEntry{holders: make(map[uint64]LockMode)}
		m.entries[row] = e
	}
	return e
}

// canGrant 判断请求与当前持有者是否兼容（忽略自己）
func (m *Manager) canGrant(e *lockEntry, txnID uint64, mode LockMode) bool {
	for h, hm := range e.holders {
		if h == txnID {
			continue
		}
		if mode == Exclusive || hm == Exclusive {
			return false
		}
	}
	return true
}

func (m *Manager) grant(e *lockEntry, row string, txnID uint64, mode LockMode) {
	e.holders[txnID] = mode
	rows, ok := m.heldBy[txnID]
	if !ok {
		rows = make(map[string]LockMode)
		m.heldBy[txnID] = rows
	}
	rows[row] = mode
}

// conflictingHolders 返回与请求冲突的持有者（不含自己）
func conflictingHolders(e *lockEntry, txnID uint64, mode LockMode) []uint64 {
	var out []uint64
	for h, hm := range e.holders {
		if h == txnID {
			continue
		}
		if mode == Exclusive || hm == Exclusive {
			out = append(out, h)
		}
	}
	return out
}

// processQueue 按 FIFO 顺序授权队首可兼容的等待请求（连续的S锁可批量授权）
func (m *Manager) processQueue(row string, e *lockEntry) {
	for len(e.queue) > 0 {
		req := e.queue[0]
		if !m.canGrant(e, req.txnID, req.mode) {
			return
		}
		e.queue = e.queue[1:]
		m.grant(e, row, req.txnID, req.mode)
		req.ch <- nil
	}
}

// abortLocked 中止事务：打中止标记、释放其全部锁、唤醒并拒绝其等待请求
func (m *Manager) abortLocked(txnID uint64) {
	m.aborted[txnID] = true
	m.releaseAllLocked(txnID)
	m.rejectWaitingLocked(txnID)
}

// rejectWaitingLocked 将事务在所有行上的等待请求移出队列并通知ErrAborted
func (m *Manager) rejectWaitingLocked(txnID uint64) {
	for _, e := range m.entries {
		for i := 0; i < len(e.queue); {
			if e.queue[i].txnID == txnID {
				req := e.queue[i]
				e.queue = append(e.queue[:i], e.queue[i+1:]...)
				req.ch <- ErrAborted
				continue
			}
			i++
		}
	}
}

// releaseAllLocked 释放事务持有的全部行锁并唤醒等待者
func (m *Manager) releaseAllLocked(txnID uint64) {
	rows, ok := m.heldBy[txnID]
	if !ok {
		return
	}
	for row := range rows {
		if e, ok := m.entries[row]; ok {
			delete(e.holders, txnID)
			m.processQueue(row, e)
		}
	}
	delete(m.heldBy, txnID)
}

func (m *Manager) removeRequest(e *lockEntry, req *request) {
	for i, r := range e.queue {
		if r == req {
			e.queue = append(e.queue[:i], e.queue[i+1:]...)
			return
		}
	}
}
