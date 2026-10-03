package occ

import "sync/atomic"

// readEnt 是 ReadSet 中的一项：key、读取时观察到的版本、记录槽缓存。
// slot 为 nil 表示读取时 key 尚不存在（版本 0），验证时需重新查表
// 以捕获其他事务并发创建该 key 的幻影。
type readEnt struct {
	key  string
	ver  uint64
	slot *atomicRecord
}

// writeEnt 是 WriteSet 中的一项：key 及待写入值。
type writeEnt struct {
	key string
	val interface{}
}

// Txn 是一个进行中的事务，持有私有的 ReadSet 与 WriteSet。
// 集合用小切片实现，避免短事务的 map 分配开销。
type Txn struct {
	e      *Engine
	reads  []readEnt
	writes []writeEnt
}

// Begin 开启一个事务（读取阶段开始）。
func (e *Engine) Begin() *Txn {
	return &Txn{e: e}
}

// Get 读取 key。先查 WriteSet（read-your-writes），再查存储并记录版本。
func (t *Txn) Get(key string) (interface{}, bool) {
	for i := len(t.writes) - 1; i >= 0; i-- {
		if t.writes[i].key == key {
			return t.writes[i].val, true
		}
	}
	slot := t.e.getSlot(key)
	var r *record
	if slot != nil {
		r = slot.load()
	}
	seen := false
	for _, re := range t.reads {
		if re.key == key {
			seen = true
			break
		}
	}
	if !seen {
		var ver uint64
		if r != nil {
			ver = r.version
		}
		t.reads = append(t.reads, readEnt{key: key, ver: ver, slot: slot})
	}
	if r == nil {
		return nil, false
	}
	return r.value, true
}

// Set 把写入缓存在 WriteSet，提交前对其他事务不可见。
func (t *Txn) Set(key string, value interface{}) {
	for i := range t.writes {
		if t.writes[i].key == key {
			t.writes[i].val = value
			return
		}
	}
	t.writes = append(t.writes, writeEnt{key: key, val: value})
}

// validateLocked 检查 ReadSet 中每个 key 的当前版本是否仍等于读取时的版本。
// 调用方需持有提交锁，或处于 seqlock 保护区间内。
func (t *Txn) validateLocked() bool {
	for _, re := range t.reads {
		slot := re.slot
		if slot == nil {
			// 读取时 key 不存在；重新查表以检测并发创建。
			slot = t.e.getSlot(re.key)
		}
		var cur uint64
		if slot != nil {
			if r := slot.load(); r != nil {
				cur = r.version
			}
		}
		if cur != re.ver {
			return false
		}
	}
	return true
}

// Commit 执行验证阶段与写入阶段。
//
// 写事务：获取自旋锁 -> 验证 ReadSet -> 分配全局版本 -> 批量发布 WriteSet。
// 纯读事务：走 seqlock 协议无锁验证（读期间无写事务完成则成功）。
// 验证失败返回 ErrConflict，WriteSet 直接丢弃，存储不受任何影响。
func (t *Txn) Commit() error {
	if len(t.writes) == 0 {
		return t.commitReadOnly()
	}

	e := t.e
	e.lock.Lock()
	defer e.lock.Unlock()

	if !t.validateLocked() {
		atomic.AddUint64(&e.conflicts, 1)
		return ErrConflict
	}

	atomic.AddUint64(&e.seq, 1) // seq 变奇：写入进行中
	ver := atomic.AddUint64(&e.ver, 1)
	for _, w := range t.writes {
		slot := e.getOrCreateSlot(w.key)
		slot.store(&record{version: ver, value: w.val})
	}
	atomic.AddUint64(&e.seq, 1) // seq 变偶：写入完成
	atomic.AddUint64(&e.commits, 1)
	return nil
}

// commitReadOnly 用 seqlock 协议做无锁验证：
// 验证前后各读一次全局序列号，期间有写事务完成则序列号变化，判定冲突。
func (t *Txn) commitReadOnly() error {
	e := t.e
	s1 := atomic.LoadUint64(&e.seq)
	if s1&1 != 0 {
		atomic.AddUint64(&e.conflicts, 1)
		return ErrConflict
	}
	ok := t.validateLocked()
	s2 := atomic.LoadUint64(&e.seq)
	if !ok || s1 != s2 {
		atomic.AddUint64(&e.conflicts, 1)
		return ErrConflict
	}
	atomic.AddUint64(&e.commits, 1)
	return nil
}

// Rollback 显式回滚。由于写入从未离开 WriteSet，回滚只是丢弃缓冲区。
func (t *Txn) Rollback() {
	t.reads = nil
	t.writes = nil
}
