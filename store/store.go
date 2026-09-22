// Package store 是模拟器使用的“数据库”：进程内的 KV 表。
//
// 它没有真正的崩溃恢复（崩溃是用 panic 跳过后续代码 + 丢弃内存态来模拟的），
// 但 Commit 是原子的：Tx 要么整体生效，要么完全不生效，因此可以在其前后
// 精确放置故障点。
package store

import "sort"

// DB 是一个持久状态（相对模拟器崩溃而言：DB 数据在崩溃后保留）。
//
// 事件循环严格单线程，因此不需要真正的并发锁；busy 仅用于在出现
// “故障 panic 后仍有事务未结束”这类编程错误时立刻暴露问题。
type DB struct {
	busy bool
	data map[string]string

	// OnCommit 在每次成功提交后调用，可用于审计提交时点。
	OnCommit func(changed []string)
}

// New 创建数据库。
func New() *DB {
	return &DB{data: map[string]string{}}
}

// Tx 是一个事务。读取基于开始时的快照，写入暂存在私有 map 中。
type Tx struct {
	db       *DB
	snapshot map[string]string
	writes   map[string]*string
	closed   bool
}

// Begin 开启事务：快照当前数据，之后在私有写入集上修改，提交时原子合并。
func (db *DB) Begin() *Tx {
	if db.busy {
		panic("store: nested Begin on single-threaded DB")
	}
	db.busy = true
	snap := make(map[string]string, len(db.data))
	for k, v := range db.data {
		snap[k] = v
	}
	return &Tx{db: db, snapshot: snap, writes: map[string]*string{}}
}

func (tx *Tx) unlock() {
	if tx.closed {
		return
	}
	tx.closed = true
	tx.db.busy = false
}

// Get 读取当前事务视图中的值；不存在返回 ("", false)。
func (tx *Tx) Get(key string) (string, bool) {
	if v, ok := tx.writes[key]; ok {
		if v == nil {
			return "", false
		}
		return *v, true
	}
	v, ok := tx.snapshot[key]
	return v, ok
}

// Put 写入键值（事务内可见，提交前对其它事务不可见）。
func (tx *Tx) Put(key, value string) {
	v := value
	tx.writes[key] = &v
}

// Delete 删除键。
func (tx *Tx) Delete(key string) {
	tx.writes[key] = nil
}

// ScanPrefix 返回所有以 prefix 开头的键值对，按键排序。
func (tx *Tx) ScanPrefix(prefix string) []KV {
	keys := tx.Keys(prefix)
	out := make([]KV, 0, len(keys))
	for _, k := range keys {
		if v, ok := tx.Get(k); ok {
			out = append(out, KV{Key: k, Value: v})
		}
	}
	return out
}

// Keys 返回当前视图中以 prefix 开头的键，按键排序。
func (tx *Tx) Keys(prefix string) []string {
	seen := map[string]struct{}{}
	for k := range tx.snapshot {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			seen[k] = struct{}{}
		}
	}
	for k, v := range tx.writes {
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		if v == nil {
			delete(seen, k)
		} else {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// KV 是扫描结果的一项。
type KV struct {
	Key   string
	Value string
}

// Commit 原子地把所有写入合并进数据库。
func (tx *Tx) Commit() {
	if tx.closed {
		panic("store: transaction already finished")
	}
	changed := make([]string, 0, len(tx.writes))
	for k, v := range tx.writes {
		changed = append(changed, k)
		if v == nil {
			delete(tx.db.data, k)
		} else {
			tx.db.data[k] = *v
		}
	}
	if tx.db.OnCommit != nil {
		sort.Strings(changed)
		tx.db.OnCommit(changed)
	}
	tx.unlock()
}

// Rollback 丢弃事务。
func (tx *Tx) Rollback() {
	tx.unlock()
}

// Read 开启只读事务执行 fn 并自动提交（不产生写入）。
func (db *DB) Read(fn func(tx *Tx)) {
	tx := db.Begin()
	defer tx.Commit()
	fn(tx)
}

// ViewKeys 返回数据库当前以 prefix 开头的键（供断言使用）。
func (db *DB) ViewKeys(prefix string) []string {
	var keys []string
	db.Read(func(tx *Tx) { keys = tx.Keys(prefix) })
	return keys
}
