// Package publisher 提供并发结果发布器：任务可乱序完成，
// 但结果严格按提交顺序交付。
//
// 语义：
//   - 每个提交的任务占用一个单调递增的序号，取消也占一个序号；
//   - 任务失败时交付携带错误的占位结果，后续结果继续交付；
//   - 容量（在途 + 已缓存未交付结果）满时，Submit 阻塞新任务启动；
//   - 槽位在结果交付后才释放，因此内存占用恒有上界 capacity；
//   - Close 后不接受新任务，但已提交任务的尾部结果会按序交付完毕。
package publisher

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed 表示发布器已关闭，不再接受新任务。
var ErrClosed = errors.New("publisher: closed")

// Result 是一次交付的结果。Err 非空表示错误占位
// （任务失败或上下文取消），此时 Value 无效。
type Result struct {
	Seq   uint64
	Value any
	Err   error
}

// Publisher 按提交顺序交付并发任务的结果。
type Publisher struct {
	slots    chan struct{} // 容量信号量：交付后才释放
	closedCh chan struct{}

	mu          sync.Mutex
	cond        sync.Cond
	nextSeq     uint64 // 下一个待分配的序号
	nextOut     uint64 // 下一个待交付的序号
	outstanding int    // 已提交但未交付的任务数
	pending     map[uint64]Result
	maxPending  int // pending 的历史峰值，用于验证内存上界
	closed      bool

	out chan Result
}

// New 创建容量为 capacity 的发布器并启动交付循环。
// capacity 同时是在途任务数与乱序缓存结果数的上界。
func New(capacity int) *Publisher {
	if capacity <= 0 {
		capacity = 1
	}
	p := &Publisher{
		slots:    make(chan struct{}, capacity),
		closedCh: make(chan struct{}),
		pending:  make(map[uint64]Result),
		out:      make(chan Result, capacity),
	}
	p.cond.L = &p.mu
	go p.run()
	return p
}

// Submit 提交任务 fn 并返回其序号。缓冲区满时阻塞，
// 直到有结果交付腾出槽位、ctx 取消或发布器关闭。
// fn 在独立 goroutine 中执行；其返回错误（含 ctx 取消）
// 会成为该序号的错误占位结果。
func (p *Publisher) Submit(ctx context.Context, fn func(context.Context) (any, error)) (uint64, error) {
	select {
	case p.slots <- struct{}{}:
	case <-p.closedCh:
		return 0, ErrClosed
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.slots
		return 0, ErrClosed
	}
	seq := p.nextSeq
	p.nextSeq++
	p.outstanding++
	p.mu.Unlock()

	go func() {
		value, err := fn(ctx)
		if err == nil && ctx.Err() != nil {
			err = ctx.Err() // 取消也占一个序号：交付取消占位
		}
		p.mu.Lock()
		p.pending[seq] = Result{Seq: seq, Value: value, Err: err}
		if len(p.pending) > p.maxPending {
			p.maxPending = len(p.pending)
		}
		p.cond.Broadcast()
		p.mu.Unlock()
	}()

	return seq, nil
}

// Results 返回按提交顺序交付结果的通道，Close 且全部交付完成后关闭。
func (p *Publisher) Results() <-chan Result {
	return p.out
}

// Close 停止接受新任务；已提交任务的结果仍会按序交付，
// 全部交付后 Results 通道关闭。
func (p *Publisher) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.closedCh)
		p.cond.Broadcast()
	}
	p.mu.Unlock()
}

// Buffered 返回当前已缓存但尚未交付的结果数。
func (p *Publisher) Buffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// MaxBuffered 返回缓存结果数的历史峰值，用于验证内存上界。
func (p *Publisher) MaxBuffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxPending
}

// run 是交付循环：等待序号 nextOut 的结果就绪后按序交付。
// 槽位在交付时才释放，保证 pending 大小永不超过容量。
func (p *Publisher) run() {
	for {
		p.mu.Lock()
		for {
			if _, ok := p.pending[p.nextOut]; ok {
				break
			}
			if p.closed && p.outstanding == 0 {
				close(p.out)
				p.mu.Unlock()
				return
			}
			p.cond.Wait()
		}
		res := p.pending[p.nextOut]
		delete(p.pending, p.nextOut)
		p.nextOut++
		p.outstanding--
		<-p.slots // 交付后才释放槽位
		p.mu.Unlock()
		p.out <- res
	}
}
