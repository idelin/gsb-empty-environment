package taskpkg

import "sync"

// lockState 维护单个对象的共享/排他锁计数。
//
// 锁管理器完全不感知能力、租户或权限：任何持有者字符串都能申请锁，
// 因此“伪造锁持有者”拿到的只是冲突控制原语，不附带任何读取授权。
type lockState struct {
	mu   sync.Mutex
	cond *sync.Cond
	// shared 记录每个持有者当前持有的共享锁次数（共享锁可重入）。
	shared map[string]int
	// exclusiveHolder 非空时表示对象处于排他锁状态。
	exclusiveHolder string
	// exclusiveDepth 记录排他锁的重入深度，解锁次数须与加锁次数对称。
	exclusiveDepth int
}

// LockManager 是按对象分片的数据库风格锁管理器。
type LockManager struct {
	mu     sync.Mutex
	locked map[string]*lockState
}

// NewLockManager 创建空的锁管理器。
func NewLockManager() *LockManager {
	return &LockManager{locked: make(map[string]*lockState)}
}

func (lm *LockManager) stateFor(objectID string) *lockState {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	st, ok := lm.locked[objectID]
	if !ok {
		st = &lockState{shared: make(map[string]int)}
		st.cond = sync.NewCond(&st.mu)
		lm.locked[objectID] = st
	}
	return st
}

// LockShared 阻塞直到持有者获得对象的共享锁，共享锁可重入。
func (lm *LockManager) LockShared(objectID, holder string) {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	for st.exclusiveHolder != "" {
		st.cond.Wait()
	}
	st.shared[holder]++
}

// TryLockShared 尝试非阻塞获取共享锁，冲突时返回 false，绝不授予任何权限。
func (lm *LockManager) TryLockShared(objectID, holder string) bool {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.exclusiveHolder != "" {
		return false
	}
	st.shared[holder]++
	return true
}

// LockExclusive 阻塞直到持有者获得对象的排他锁，排他锁对同一持有者可重入。
func (lm *LockManager) LockExclusive(objectID, holder string) {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	for {
		if st.exclusiveHolder == holder {
			// 排他锁重入：持有者保持同一把排他锁，解锁次数对称即可。
			st.exclusiveDepth++
			return
		}
		if st.exclusiveHolder == "" && len(st.shared) == 0 {
			st.exclusiveHolder = holder
			st.exclusiveDepth = 1
			return
		}
		st.cond.Wait()
	}
}

// TryLockExclusive 尝试非阻塞获取排他锁，冲突时返回 false。
func (lm *LockManager) TryLockExclusive(objectID, holder string) bool {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.exclusiveHolder == holder {
		return true
	}
	if st.exclusiveHolder != "" || len(st.shared) != 0 {
		return false
	}
	st.exclusiveHolder = holder
	st.exclusiveDepth = 1
	return true
}

// Upgrade 把持有者已持有的共享锁升级为排他锁。
//
// 升级必须等待其他共享持有者释放；锁升级期间不改变任何能力的权限。
func (lm *LockManager) Upgrade(objectID, holder string) error {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.exclusiveHolder == holder {
		return nil
	}
	if st.shared[holder] == 0 {
		return ErrNotLocked
	}
	for {
		onlyHolder := len(st.shared) == 1 && st.shared[holder] > 0
		if onlyHolder && st.exclusiveHolder == "" {
			delete(st.shared, holder)
			st.exclusiveHolder = holder
			st.exclusiveDepth = 1
			return nil
		}
		st.cond.Wait()
	}
}

// Unlock 释放一次锁。共享锁按计数递减，归零后删除持有者记录。
func (lm *LockManager) Unlock(objectID, holder string) error {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.exclusiveHolder == holder {
		st.exclusiveDepth--
		if st.exclusiveDepth == 0 {
			st.releaseExclusive()
		}
		return nil
	}
	if n := st.shared[holder]; n > 0 {
		if n == 1 {
			delete(st.shared, holder)
		} else {
			st.shared[holder] = n - 1
		}
		st.cond.Broadcast()
		return nil
	}
	return ErrNotLocked
}

// releaseExclusive 释放排他锁并唤醒所有等待者。
func (st *lockState) releaseExclusive() {
	st.exclusiveHolder = ""
	st.cond.Broadcast()
}

// HoldsExclusive 报告持有者当前是否持有对象的排他锁。
func (lm *LockManager) HoldsExclusive(objectID, holder string) bool {
	st := lm.stateFor(objectID)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.exclusiveHolder == holder
}
