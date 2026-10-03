// Package lockkv 是基于读写锁的悲观并发 KV 存储，用作 OCC 的对照基线。
// 事务在整个生命周期内持有全局锁：读事务持 RLock，写事务持 Lock。
package lockkv

import "sync"

// Store 用一把全局 RWMutex 保护所有数据。
type Store struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

// New 创建空存储。
func New() *Store {
	return &Store{data: make(map[string]interface{})}
}

// Txn 是一个持锁事务。Commit/Rollback 之前一直占用全局锁。
type Txn struct {
	s     *Store
	write bool
	done  bool
}

// Begin 开启事务。write=true 时获取独占写锁，否则获取读锁。
func (s *Store) Begin(write bool) *Txn {
	t := &Txn{s: s, write: write}
	if write {
		s.mu.Lock()
	} else {
		s.mu.RLock()
	}
	return t
}

// Get 读一个 key。
func (t *Txn) Get(key string) (interface{}, bool) {
	v, ok := t.s.data[key]
	return v, ok
}

// Set 写一个 key（仅写事务可用）。
func (t *Txn) Set(key string, value interface{}) {
	t.s.data[key] = value
}

// Commit 提交并释放锁。
func (t *Txn) Commit() {
	if t.done {
		return
	}
	t.done = true
	if t.write {
		t.s.mu.Unlock()
	} else {
		t.s.mu.RUnlock()
	}
}

// Rollback 回滚并释放锁。写直达 map 前未做缓冲的场景下由调用方保证不写。
func (t *Txn) Rollback() {
	t.Commit()
}
