// Package workchan 提供泛型有界通道，用于在 goroutine 之间传递工作项。
//
// 同步策略说明
//
// 实现采用单一互斥锁（sync.Mutex）保护全部内部状态：缓冲区（切片实现的
// FIFO 队列，而非 Go 内置 channel）、关闭标志、阻塞发送者队列 sendQ、
// 阻塞接收者队列 recvQ。所有操作的线性化点都发生在持锁期间的状态变更
// 瞬间，因此取消与成功交付的竞争有唯一结果：
//
//   - 等待者（waiter）携带一个容量为 1 的通知 channel。完成方在持锁时
//     先写结果字段、置 completed=true，再向通知 channel 发送（缓冲为 1，
//     持锁发送不会阻塞）。通知 channel 的 happens-before 边保证等待方
//     无锁读取结果字段是安全的。
//   - 等待方在 select 中同时监听 ctx.Done() 与通知 channel。若 ctx 先
//     触发，等待方重新持锁：若 completed 已置位，则按已完成的结果返回
//     （交付已经发生，不可撤销）；否则置 canceled=true 并返回 ctx.Err()。
//     完成方从队列中弹出 waiter 时跳过 canceled 的节点。由于双方都在
//     同一把锁下检查/置位，"已交付"与"已取消"两个结局互斥且唯一：
//     发送若返回取消，其值永远不会被接收；接收若返回取消，绝不会吞掉
//     任何已交付的值。
//
// 容量为 0 时缓冲区恒为空，发送者只能在 sendQ 中等待，直到某个接收者
// 在持锁时直接取走其值（rendezvous 配对），双方在同一线性化点完成。
//
// Close 幂等：首次调用置 closed 标志并唤醒全部阻塞发送者（以 ErrClosed
// 拒绝，其值不交付）与全部阻塞接收者（返回关闭标记）；此后 Send 一律
// 返回 ErrClosed，Recv 继续按 FIFO 顺序排空缓冲区后才返回关闭标记。
//
// 已知取舍：被取消的 waiter 采用惰性删除（弹出时跳过），若队列头部长期
// 堆积已取消节点且再无新操作，节点内存不会立即回收；FIFO 仅对"成功入队
// 且未被取消"的等待者严格成立。
package workchan

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed 是通道关闭后 Send 返回的错误；被 Close 唤醒的阻塞发送者
// 同样以 ErrClosed 结束，其值不会被交付。
var ErrClosed = errors.New("workchan: channel is closed")

// Chan 是容量有界的泛型通道，容量可以为 0（rendezvous 语义）。
type Chan[T any] struct {
	mu     sync.Mutex
	buf    []T // FIFO 数据队列（切片实现，非内置 channel）
	cap    int
	closed bool
	sendQ  []*sendWaiter[T]
	recvQ  []*recvWaiter[T]

	// 测试钩子：waiter 入队后、解锁前调用。仅测试使用，
	// 回调在持锁期间执行，不得阻塞或回调进通道。
	hookSendEnqueued func()
	hookRecvEnqueued func()
}

type sendWaiter[T any] struct {
	val       T
	err       error // nil=已交付，ErrClosed=被关闭拒绝
	ch        chan struct{}
	completed bool
	canceled  bool
}

type recvWaiter[T any] struct {
	val       T
	ok        bool // false 表示关闭
	ch        chan struct{}
	completed bool
	canceled  bool
}

// New 创建容量为 capacity 的通道。capacity 必须 >= 0。
func New[T any](capacity int) *Chan[T] {
	if capacity < 0 {
		panic("workchan: negative capacity")
	}
	return &Chan[T]{cap: capacity}
}

// Send 将 v 送入通道。返回 nil 表示值已交付（进入缓冲区或直接交给
// 接收者）；返回 ErrClosed 表示通道已关闭，值未交付；返回 ctx.Err()
// 表示等待期间被取消，值保证不会在此后被任何接收者收到。
func (c *Chan[T]) Send(ctx context.Context, v T) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	// 有阻塞接收者：直接交接，跳过已取消的节点。
	for len(c.recvQ) > 0 {
		w := c.recvQ[0]
		c.recvQ = c.recvQ[1:]
		if w.canceled {
			continue
		}
		w.val = v
		w.ok = true
		w.completed = true
		w.ch <- struct{}{}
		c.mu.Unlock()
		return nil
	}
	if len(c.buf) < c.cap {
		c.buf = append(c.buf, v)
		c.mu.Unlock()
		return nil
	}
	w := &sendWaiter[T]{val: v, ch: make(chan struct{}, 1)}
	c.sendQ = append(c.sendQ, w)
	if c.hookSendEnqueued != nil {
		c.hookSendEnqueued()
	}
	c.mu.Unlock()

	select {
	case <-w.ch:
		return w.err
	case <-ctx.Done():
		c.mu.Lock()
		if w.completed {
			err := w.err
			c.mu.Unlock()
			return err
		}
		w.canceled = true
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Recv 从通道接收一个值。返回 (v, true, nil) 表示成功交付；
// 返回 (zero, false, nil) 表示通道已关闭且缓冲区已排空；
// 返回 (zero, false, ctx.Err()) 表示等待期间被取消，此时保证
// 没有吞掉任何已交付的值。
func (c *Chan[T]) Recv(ctx context.Context) (T, bool, error) {
	c.mu.Lock()
	if len(c.buf) > 0 {
		v := c.buf[0]
		var zero T
		c.buf[0] = zero
		c.buf = c.buf[1:]
		// 缓冲区腾出位置：把队首阻塞发送者的值移入缓冲区，
		// 保持全局 FIFO 顺序。
		for len(c.sendQ) > 0 {
			w := c.sendQ[0]
			c.sendQ = c.sendQ[1:]
			if w.canceled {
				continue
			}
			c.buf = append(c.buf, w.val)
			w.completed = true
			w.ch <- struct{}{}
			break
		}
		c.mu.Unlock()
		return v, true, nil
	}
	// 缓冲区为空：与阻塞发送者直接配对（容量 0 的 rendezvous 路径）。
	for len(c.sendQ) > 0 {
		w := c.sendQ[0]
		c.sendQ = c.sendQ[1:]
		if w.canceled {
			continue
		}
		v := w.val
		w.completed = true
		w.ch <- struct{}{}
		c.mu.Unlock()
		return v, true, nil
	}
	if c.closed {
		c.mu.Unlock()
		var zero T
		return zero, false, nil
	}
	w := &recvWaiter[T]{ch: make(chan struct{}, 1)}
	c.recvQ = append(c.recvQ, w)
	if c.hookRecvEnqueued != nil {
		c.hookRecvEnqueued()
	}
	c.mu.Unlock()

	select {
	case <-w.ch:
		return w.val, w.ok, nil
	case <-ctx.Done():
		c.mu.Lock()
		if w.completed {
			v, ok := w.val, w.ok
			c.mu.Unlock()
			return v, ok, nil
		}
		w.canceled = true
		c.mu.Unlock()
		var zero T
		return zero, false, ctx.Err()
	}
}

// Close 关闭通道，幂等。关闭后：新的 Send 返回 ErrClosed；阻塞的
// 发送者被唤醒并以 ErrClosed 结束（值不交付）；阻塞的接收者被唤醒
// 并返回关闭标记；缓冲区中已有的值仍可被后续 Recv 按顺序取完。
func (c *Chan[T]) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	for _, w := range c.sendQ {
		if w.canceled || w.completed {
			continue
		}
		w.err = ErrClosed
		w.completed = true
		w.ch <- struct{}{}
	}
	c.sendQ = nil
	for _, w := range c.recvQ {
		if w.canceled || w.completed {
			continue
		}
		w.ok = false
		w.completed = true
		w.ch <- struct{}{}
	}
	c.recvQ = nil
	c.mu.Unlock()
}
