// Package publisher 实现并发结果发布器。
//
// 任务并发执行、允许乱序完成，但结果严格按提交顺序交付；
// 失败、取消或 panic 的任务以错误占位形式占据其序号，后续结果照常交付；
// 缓冲区（运行中任务 + 待交付结果）满时新的提交会被阻塞，
// 因此内存占用始终有界；Close 之后已提交的结果（包括尾部）不会丢失。
package publisher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrClosed 表示发布器已关闭，不再接受新任务。
var ErrClosed = errors.New("publisher: closed")

// Task 是一个可取消的计算任务。
type Task func(ctx context.Context) (any, error)

// Result 是任务的交付结果。Err 非空表示失败/取消/panic 占位。
type Result struct {
	Seq   int   // 提交序号，从 0 开始递增
	Value any   // 任务成功时的返回值
	Err   error // 失败、取消或 panic 时的错误占位
}

// Publisher 并发执行任务并按提交顺序发布结果。
type Publisher struct {
	sem chan struct{} // 信号量：容量 = 运行中任务 + 待交付结果 的上限
	in  chan Result   // 工作者 -> 派发器；容量等于 sem 容量，发送永不阻塞
	out chan Result   // 派发器 -> 消费者

	closing chan struct{} // Close 时关闭，唤醒阻塞中的 Submit

	mu      sync.Mutex
	closed  bool
	nextSeq int

	wg        sync.WaitGroup // 运行中的任务
	closeOnce sync.Once

	pending atomic.Int64 // 已完成但尚未交付的结果数
}

// New 创建一个发布器。capacity 为缓冲区容量：任意时刻
// 「运行中任务 + 已完成待交付结果」的总数不超过 capacity，
// 超出后 Submit 会阻塞，从而保证内存占用有界。
func New(capacity int) *Publisher {
	if capacity <= 0 {
		capacity = 1
	}
	p := &Publisher{
		sem:     make(chan struct{}, capacity),
		in:      make(chan Result, capacity),
		out:     make(chan Result),
		closing: make(chan struct{}),
	}
	go p.dispatch()
	return p
}

// Submit 提交一个任务并返回其序号。
// 缓冲区满时阻塞，直到有空位、ctx 被取消或发布器关闭。
func (p *Publisher) Submit(ctx context.Context, task Task) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if task == nil {
		return -1, errors.New("publisher: nil task")
	}

	// 缓冲区满则在此阻塞，阻止新任务启动。
	select {
	case p.sem <- struct{}{}:
	case <-p.closing:
		return -1, ErrClosed
	case <-ctx.Done():
		return -1, ctx.Err()
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.sem
		return -1, ErrClosed
	}
	seq := p.nextSeq
	p.nextSeq++
	p.wg.Add(1)
	p.mu.Unlock()

	go p.run(seq, ctx, task)
	return seq, nil
}

// Results 返回结果通道。结果严格按提交序号交付；
// Close 后所有已提交结果交付完毕，通道随即关闭。
func (p *Publisher) Results() <-chan Result {
	return p.out
}

// Pending 返回已完成但尚未交付的结果数量。
func (p *Publisher) Pending() int64 {
	return p.pending.Load()
}

// Close 停止接受新任务。已提交的任务继续执行，
// 其结果（包括尾部结果）仍会按序交付，全部交付后结果通道关闭。
// Close 本身不等待任务完成，可重复调用。
func (p *Publisher) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		close(p.closing)
		go func() {
			p.wg.Wait()
			close(p.in)
		}()
	})
}

// run 执行单个任务并上报结果。每个序号恰好上报一次：
// 正常返回、失败、panic 或取消都会生成对应的结果/占位。
func (p *Publisher) run(seq int, ctx context.Context, task Task) {
	defer p.wg.Done()

	done := make(chan Result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- Result{Seq: seq, Err: fmt.Errorf("publisher: task %d panicked: %v", seq, r)}
			}
		}()
		v, err := task(ctx)
		done <- Result{Seq: seq, Value: v, Err: err}
	}()

	var r Result
	select {
	case r = <-done:
	case <-ctx.Done():
		// 取消同样占一个序号：交付取消占位，遗弃仍在运行的任务体。
		r = Result{Seq: seq, Err: ctx.Err()}
	}
	p.pending.Add(1)
	p.in <- r
}

// dispatch 是唯一写 out 的 goroutine，负责把乱序完成的结果
// 重排为提交顺序交付，并在交付后才释放缓冲槽位。
func (p *Publisher) dispatch() {
	defer close(p.out)
	pending := make(map[int]Result)
	next := 0

	flush := func() {
		for {
			head, ok := pending[next]
			if !ok {
				return
			}
			p.out <- head // 阻塞即对消费者施加背压；in 有缓冲，工作者不会因此阻塞
			delete(pending, next)
			<-p.sem // 交付后才释放槽位：待交付结果始终占用缓冲容量
			p.pending.Add(-1)
			next++
		}
	}

	for r := range p.in {
		pending[r.Seq] = r
		flush()
	}
	// in 关闭意味着所有已提交任务都已上报，冲刷尾部，一个也不丢。
	flush()
}
