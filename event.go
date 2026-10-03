// Package cdcundo 实现反向补偿重放与数据回滚包生成器：
// 基于 CDC（Change Data Capture）前后镜像，在人为误操作后
// 逆向生成补偿 DML 序列，将数据快速冲正回误操作前的状态。
package cdcundo

import (
	"fmt"
	"sort"
	"time"
)

// Op 表示 CDC 捕获到的变更操作类型。
type Op int

const (
	OpInsert Op = iota
	OpUpdate
	OpDelete
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "INSERT"
	case OpUpdate:
		return "UPDATE"
	case OpDelete:
		return "DELETE"
	}
	return "UNKNOWN"
}

// Row 表示一行数据：列名 -> 值。
type Row map[string]interface{}

// Clone 返回行的深拷贝（值为标量，浅拷贝即可）。
func (r Row) Clone() Row {
	if r == nil {
		return nil
	}
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// Equal 比较两行是否完全一致（列集合与值均相同）。
func (r Row) Equal(other Row) bool {
	if len(r) != len(other) {
		return false
	}
	for k, v := range r {
		ov, ok := other[k]
		if !ok || fmt.Sprintf("%#v", v) != fmt.Sprintf("%#v", ov) {
			return false
		}
	}
	return true
}

// SortedKeys 返回排序后的列名，保证 SQL 渲染输出确定。
func (r Row) SortedKeys() []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ChangeEvent 是一条 CDC 日志记录，携带前置镜像与后置镜像。
type ChangeEvent struct {
	Seq       uint64    // 日志内全局单调递增序号
	TxnID     string    // 产生该变更的事务 ID
	Timestamp time.Time // 毫秒级时间戳
	Table     string    // 表名
	Op        Op        // 操作类型
	Key       Row       // 主键列
	Before    Row       // 前置镜像（INSERT 时为 nil）
	After     Row       // 后置镜像（DELETE 时为 nil）
}

// CDCLog 是只追加的变更日志，线程安全。
type CDCLog struct {
	events []ChangeEvent
	seq    uint64
}

// NewCDCLog 创建空的 CDC 日志。
func NewCDCLog() *CDCLog {
	return &CDCLog{}
}

// Append 追加一条变更事件，自动分配序号。
func (l *CDCLog) Append(e ChangeEvent) ChangeEvent {
	l.seq++
	e.Seq = l.seq
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	// 统一截断到毫秒，保证回滚窗口可按毫秒级精度圈定。
	e.Timestamp = e.Timestamp.Truncate(time.Millisecond)
	l.events = append(l.events, e)
	return e
}

// Events 返回日志中全部事件的拷贝（按 Seq 升序）。
func (l *CDCLog) Events() []ChangeEvent {
	out := make([]ChangeEvent, len(l.events))
	copy(out, l.events)
	return out
}

// Len 返回日志中的事件条数。
func (l *CDCLog) Len() int {
	return len(l.events)
}
