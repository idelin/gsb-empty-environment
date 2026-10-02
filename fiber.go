package memsched

import "sync/atomic"

// Task 是协程体。通过 ctx 申请/释放的临时对象会被监控器精确统计。
type Task func(ctx *Context) error

// Fiber 是调度器管理的最小执行单元（一个受监控的 goroutine 任务）。
type Fiber struct {
	ID uint64

	reclaimCapable bool // 具备 GC 回收能力：运行时能主动释放缓存/临时对象

	used atomic.Int64 // 当前持有的的临时对象字节数

	task Task
	done chan struct{}
	err  error
}

// Used 返回该协程当前持有的临时对象字节数。
func (f *Fiber) Used() int64 { return f.used.Load() }

// ReclaimCapable 报告该协程是否具备 GC 回收能力。
func (f *Fiber) ReclaimCapable() bool { return f.reclaimCapable }

// Wait 阻塞直到协程结束并返回其错误。
func (f *Fiber) Wait() error {
	<-f.done
	return f.err
}

// Context 是协程运行期上下文，所有内存申请都应经过它以便统计。
type Context struct {
	Fiber   *Fiber
	monitor *Monitor
}

// Alloc 申请 n 字节临时对象（受全局配额约束，超额时阻塞等待）。
func (c *Context) Alloc(n int64) { c.monitor.Alloc(c.Fiber, n) }

// Release 释放 n 字节临时对象。
func (c *Context) Release(n int64) { c.monitor.Release(c.Fiber, n) }
