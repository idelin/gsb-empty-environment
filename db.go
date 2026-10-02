// Package skewguard 提供写倾斜（write skew）检测与声明式约束断言。
//
// 业务不变量（如「医生值班表至少 1 人值班」这类跨多行的约束）被登记为
// 显式谓词；事务在提交阶段基于「最新已提交状态 + 本事务写集」重验谓词，
// 从而把快照隔离下不可见的并发交叉修改（写倾斜）物化为可检测的冲突，
// 并安全回滚破坏不变量的事务。
package skewguard

import "sync"

// Constraint 是一条全局声明式约束：对任意视图求值，返回 true 表示不变量成立。
type Constraint struct {
	Name  string
	Check func(v View) bool
}

// DB 是一个支持谓词重验的内存键值库。
// 提交在互斥锁下串行化，因此提交时的谓词重验等价于可串行化判定。
type DB struct {
	mu          sync.Mutex
	data        map[string]string
	constraints []Constraint
}

// New 创建一个空库。
func New() *DB {
	return &DB{data: make(map[string]string)}
}

// RegisterConstraint 登记一条全局不变量，之后每次提交都会重验它。
func (db *DB) RegisterConstraint(name string, check func(v View) bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.constraints = append(db.constraints, Constraint{Name: name, Check: check})
}

// Begin 开启一个事务，并为其拍摄一致性快照。
func (db *DB) Begin() *Tx {
	db.mu.Lock()
	snap := make(map[string]string, len(db.data))
	for k, v := range db.data {
		snap[k] = v
	}
	db.mu.Unlock()
	return &Tx{
		db:       db,
		snapshot: snap,
		writes:   make(map[string]*string),
	}
}

// Get 直接读取已提交状态（管理/初始化用，不参与事务）。
func (db *DB) Get(key string) (string, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	v, ok := db.data[key]
	return v, ok
}

// Set 直接写入已提交状态（管理/初始化用，不参与事务）。
func (db *DB) Set(key, value string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.data[key] = value
}

// ScanCommitted 直接扫描已提交状态（管理/校验用）。
func (db *DB) ScanCommitted(prefix string) []KV {
	db.mu.Lock()
	defer db.mu.Unlock()
	return overlayView{base: db.data}.Scan(prefix)
}
