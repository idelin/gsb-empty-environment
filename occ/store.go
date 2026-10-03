// Package occ 实现经典三阶段（读取/验证/写入）乐观并发控制引擎。
//
// 核心不变式：
//   - 每条记录带单调递增的版本号，读取阶段把 (key -> version) 记入 ReadSet；
//   - 写入全部缓存在事务私有的 WriteSet 中，验证通过前对存储不可见；
//   - 验证阶段发现 ReadSet 中任意 key 的版本已被其他已提交事务推进，
//     则返回 ErrConflict 并丢弃 WriteSet —— 回滚即"什么都不做"，干净利落；
//   - 写事务的验证+写入在全局自旋锁内一次性批量完成，是唯一的临界区；
//   - 纯读事务的验证走 seqlock 协议，完全无锁、无阻塞。
package occ

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrConflict 表示验证失败：ReadSet 中至少一个 key 的版本已过期。
var ErrConflict = errors.New("occ: validation failed, read set version stale")

// record 是一条不可变的版本化记录。value 在写入后不可变，
// 新版本通过原子替换整条 record 发布，读路径无需加锁。
type record struct {
	version uint64
	value   interface{}
}

// Engine 是 OCC 存储引擎。
type Engine struct {
	data sync.Map // key -> *atomicRecord；读免锁，槽位只增不减
	ver  uint64   // 全局提交版本计数器
	seq  uint64   // seqlock 序列号：写中为奇数，稳定为偶数
	lock spinlock // 提交阶段自旋锁

	commits   uint64 // 成功提交计数
	conflicts uint64 // 验证失败计数
}

// New 创建空引擎。
func New() *Engine {
	return &Engine{}
}

// Stats 返回累计的提交数与冲突数。
func (e *Engine) Stats() (commits, conflicts uint64) {
	return atomic.LoadUint64(&e.commits), atomic.LoadUint64(&e.conflicts)
}

// ConflictRate 返回冲突率 conflicts/(commits+conflicts)。
func (e *Engine) ConflictRate() float64 {
	c, f := e.Stats()
	if c+f == 0 {
		return 0
	}
	return float64(f) / float64(c+f)
}

// getSlot 返回 key 对应的记录槽，不存在则返回 nil（不创建）。读路径免锁。
func (e *Engine) getSlot(key string) *atomicRecord {
	if v, ok := e.data.Load(key); ok {
		return v.(*atomicRecord)
	}
	return nil
}

// getOrCreateSlot 返回 key 对应的记录槽，不存在则创建（仅写路径调用）。
func (e *Engine) getOrCreateSlot(key string) *atomicRecord {
	if v, ok := e.data.Load(key); ok {
		return v.(*atomicRecord)
	}
	slot := &atomicRecord{}
	actual, _ := e.data.LoadOrStore(key, slot)
	return actual.(*atomicRecord)
}
