package skewguard

import (
	"errors"
	"fmt"
)

// ErrTxDone 表示事务已提交或已回滚。
var ErrTxDone = errors.New("skewguard: transaction already finished")

// ConstraintError 表示提交阶段检测到不变量被破坏，事务被安全回滚。
type ConstraintError struct {
	Constraint string // 被破坏的约束/断言名称
	Scope      string // "assertion"（事务级断言）或 "invariant"（全局约束）
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("skewguard: commit aborted: %s %q violated", e.Scope, e.Constraint)
}

// IsConstraintError 判断 err 是否为约束破坏导致的回滚。
func IsConstraintError(err error) bool {
	var ce *ConstraintError
	return errors.As(err, &ce)
}

type assertion struct {
	name  string
	check func(v View) bool
}

// Tx 是一个事务：读自己的快照与写集，提交前重验全部断言与全局约束。
type Tx struct {
	db         *DB
	snapshot   map[string]string
	writes     map[string]*string // nil 表示删除
	assertions []assertion
	done       bool
}

// view 返回事务执行期视图：快照 + 自身写集（read-your-writes）。
func (tx *Tx) view() View {
	return overlayView{base: tx.snapshot, writes: tx.writes}
}

// Get 读取键：优先看到本事务的未提交写，否则看到快照。
func (tx *Tx) Get(key string) (string, bool) {
	return tx.view().Get(key)
}

// Scan 按前缀扫描事务视图，结果按键排序。
func (tx *Tx) Scan(prefix string) []KV {
	return tx.view().Scan(prefix)
}

// Set 缓冲一次写入，提交时才生效。
func (tx *Tx) Set(key, value string) {
	tx.writes[key] = &value
}

// Delete 缓冲一次删除，提交时才生效。
func (tx *Tx) Delete(key string) {
	tx.writes[key] = nil
}

// Assert 声明一个事务级谓词断言：
// 立即在事务视图上求值（快速失败），并在提交阶段
// 基于「最新已提交状态 + 本事务写集」重验——这是写倾斜检测的核心。
func (tx *Tx) Assert(name string, check func(v View) bool) error {
	if tx.done {
		return ErrTxDone
	}
	if !check(tx.view()) {
		return &ConstraintError{Constraint: name, Scope: "assertion"}
	}
	tx.assertions = append(tx.assertions, assertion{name: name, check: check})
	return nil
}

// Commit 串行化地重验所有断言与全局约束，全部通过才落盘；
// 任一谓词失败则整体回滚，已提交状态不受任何影响。
func (tx *Tx) Commit() error {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.done {
		return ErrTxDone
	}
	tx.done = true

	// 关键：谓词不再作用于事务开始时的快照，而是作用于
	// 「最新已提交状态 + 本事务写集」，从而看见并发事务已提交的交叉修改。
	cv := overlayView{base: tx.db.data, writes: tx.writes}
	for _, a := range tx.assertions {
		if !a.check(cv) {
			return &ConstraintError{Constraint: a.name, Scope: "assertion"}
		}
	}
	for _, c := range tx.db.constraints {
		if !c.Check(cv) {
			return &ConstraintError{Constraint: c.Name, Scope: "invariant"}
		}
	}
	for k, w := range tx.writes {
		if w == nil {
			delete(tx.db.data, k)
		} else {
			tx.db.data[k] = *w
		}
	}
	return nil
}

// Rollback 显式放弃事务（提交前的缓冲写被丢弃）。
func (tx *Tx) Rollback() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	return nil
}
