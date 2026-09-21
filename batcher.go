// Package batcher 提供并发安全的请求批处理器。
//
// 触发规则：积累到 MaxSize 个存活请求，或自首个请求入队起等待 MaxWait
// 超时，两者先到先触发。触发时当前待处理请求被封存为一个批次，之后提交
// 的请求进入下一批次，不会混入已封存批次。
//
// 取消语义：在批次封存前已取消的请求会从批中移除并获得取消错误；若整批
// 在封存时已全部取消，则不会调用执行函数。
//
// 失败语义：执行函数返回错误时，该错误会映射给批内每一个存活请求。
package batcher

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrClosed 在 Batcher 关闭后仍有提交时返回。
var ErrClosed = errors.New("batcher: closed")

// ErrResultCountMismatch 在执行函数返回的结果数与批内请求数不一致时返回。
var ErrResultCountMismatch = errors.New("batcher: exec returned result count mismatch")

// Result 是单个请求的最终结果，Value 与 Err 至多一个非零。
type Result struct {
	Value any
	Err   error
}

// ExecFunc 处理一个已封存批次。items 与批内存活请求一一对应且顺序一致；
// 返回值必须与 items 等长（按序对应），或返回非 nil error 表示整批失败。
type ExecFunc func(ctx context.Context, items []any) ([]any, error)

// Timer 抽象可停止的定时器，便于测试注入假时钟。
type Timer interface {
	Stop() bool
}

// Options 配置 Batcher。NewTimer 为 nil 时使用真实时钟。
type Options struct {
	MaxSize  int
	MaxWait  time.Duration
	Exec     ExecFunc
	NewTimer func(d time.Duration, f func()) Timer
}

type request struct {
	ctx  context.Context
	item any
	ch   chan Result
}

// Batcher 并发安全，可从多个 goroutine 同时 Submit。
type Batcher struct {
	maxSize  int
	maxWait  time.Duration
	exec     ExecFunc
	newTimer func(d time.Duration, f func()) Timer

	mu      sync.Mutex
	pending []*request
	timer   Timer
	closed  bool
}

// New 构造 Batcher。MaxSize 必须 >= 1，Exec 必须非 nil。
func New(opts Options) *Batcher {
	if opts.MaxSize < 1 {
		panic("batcher: MaxSize must be >= 1")
	}
	if opts.Exec == nil {
		panic("batcher: Exec must not be nil")
	}
	newTimer := opts.NewTimer
	if newTimer == nil {
		newTimer = func(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
	}
	return &Batcher{
		maxSize:  opts.MaxSize,
		maxWait:  opts.MaxWait,
		exec:     opts.Exec,
		newTimer: newTimer,
	}
}

// Submit 提交一个请求，返回缓冲容量为 1 的结果通道，
// 该通道必然恰好收到一个 Result，且不会被重复写入。
func (b *Batcher) Submit(ctx context.Context, item any) <-chan Result {
	ch := make(chan Result, 1)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		ch <- Result{Err: ErrClosed}
		return ch
	}
	if err := ctx.Err(); err != nil {
		b.mu.Unlock()
		ch <- Result{Err: err}
		return ch
	}

	b.pending = append(b.pending, &request{ctx: ctx, item: item, ch: ch})
	if len(b.pending) == 1 && b.maxWait > 0 {
		// 首项入队时启动等待定时器。
		b.timer = b.newTimer(b.maxWait, b.onTimeout)
	}
	if len(b.pending) >= b.maxSize {
		b.sealLocked()
	}
	b.mu.Unlock()
	return ch
}

// Close 拒绝后续提交，并立即封存当前未满的待处理批次。
func (b *Batcher) Close() {
	b.mu.Lock()
	b.closed = true
	b.sealLocked()
	b.mu.Unlock()
}

func (b *Batcher) onTimeout() {
	b.mu.Lock()
	b.sealLocked()
	b.mu.Unlock()
}

// sealLocked 封存当前待处理请求并异步执行。调用者必须持有 b.mu。
func (b *Batcher) sealLocked() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	batch := b.pending
	b.pending = nil
	if len(batch) == 0 {
		return
	}
	go b.run(batch)
}

// run 过滤已取消请求后执行批次，并向每个请求投递结果。
func (b *Batcher) run(batch []*request) {
	alive := make([]*request, 0, len(batch))
	for _, r := range batch {
		if err := r.ctx.Err(); err != nil {
			r.ch <- Result{Err: err}
			continue
		}
		alive = append(alive, r)
	}
	if len(alive) == 0 {
		// 全批取消：不调用执行函数。
		return
	}

	items := make([]any, len(alive))
	for i, r := range alive {
		items[i] = r.item
	}

	values, err := b.exec(context.Background(), items)
	if err != nil {
		// 批执行失败：错误映射到每个存活请求。
		for _, r := range alive {
			r.ch <- Result{Err: err}
		}
		return
	}
	if len(values) != len(alive) {
		for _, r := range alive {
			r.ch <- Result{Err: ErrResultCountMismatch}
		}
		return
	}
	for i, r := range alive {
		r.ch <- Result{Value: values[i]}
	}
}
