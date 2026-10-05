// Package mpmcq 提供有界多生产者多消费者队列，区分正常关闭（优雅排空）
// 与故障关闭（立即停止并返回余项）。两种关闭竞争时按首次生效者决定语义。
// 取消操作不会吞掉元素；队列记录可重放的操作历史，并提供容量与所有权守恒检查。
package mpmcq

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrClosed 表示正常关闭已生效：不再接受新元素，但已有元素仍可被消费完。
	ErrClosed = errors.New("mpmcq: queue gracefully closed, no new sends accepted")
	// ErrAborted 表示故障关闭已生效：队列立即停止，缓冲余项已由 Abort 调用者取回。
	ErrAborted = errors.New("mpmcq: queue aborted, remaining items returned to abort caller")
)

type state int

const (
	stateOpen state = iota
	stateClosing
	stateAborted
)

// Queue 是有界 MPMC 队列。零值不可用，请使用 New 构造。
type Queue[T any] struct {
	mu     sync.Mutex
	buf    []T // 环形缓冲，长度即容量
	head   int
	size   int
	st     state
	change chan struct{} // 状态每次变化时关闭并重建，用于广播唤醒

	seq  uint64
	hist []Event

	accepted uint64 // 已入队元素总数
	consumed uint64 // 已出队元素总数
	returned uint64 // 故障关闭时作为余项返回的元素总数
}

// New 创建容量为 capacity 的队列。capacity 必须为正。
func New[T any](capacity int) *Queue[T] {
	if capacity <= 0 {
		panic("mpmcq: capacity must be positive")
	}
	return &Queue[T]{
		buf:    make([]T, capacity),
		change: make(chan struct{}),
	}
}

// broadcastLocked 唤醒所有阻塞的发送者与接收者。调用时必须持有锁。
func (q *Queue[T]) broadcastLocked() {
	close(q.change)
	q.change = make(chan struct{})
}

// recordLocked 追加一条历史事件。调用时必须持有锁。
func (q *Queue[T]) recordLocked(kind EventKind, n int) {
	q.seq++
	q.hist = append(q.hist, Event{Seq: q.seq, Kind: kind, N: n})
}

// Send 将元素放入队列，必要时阻塞等待空位。
//
// 元素只有在真正入队后才算被队列接管；在此之前所有权始终属于调用者：
//   - 等待空位期间 ctx 被取消：元素未入队，随错误返回给调用者，不会被吞掉；
//   - 正常关闭已生效：返回 ErrClosed，元素未入队；
//   - 故障关闭已生效：返回 ErrAborted，元素未入队。
func (q *Queue[T]) Send(ctx context.Context, v T) error {
	for {
		q.mu.Lock()
		switch q.st {
		case stateAborted:
			q.mu.Unlock()
			return ErrAborted
		case stateClosing:
			q.mu.Unlock()
			return ErrClosed
		}
		if q.size < len(q.buf) {
			tail := (q.head + q.size) % len(q.buf)
			q.buf[tail] = v
			q.size++
			q.accepted++
			q.recordLocked(EvSend, 1)
			q.broadcastLocked()
			q.mu.Unlock()
			return nil
		}
		ch := q.change
		q.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			q.mu.Lock()
			q.recordLocked(EvSendCancel, 1)
			q.mu.Unlock()
			return ctx.Err()
		}
	}
}

// Recv 从队列取出元素，必要时阻塞等待。
//
//   - 正常关闭生效后：继续返回缓冲中的剩余元素，排空后返回 ErrClosed；
//   - 故障关闭生效后：立即返回 ErrAborted（余项归 Abort 调用者）；
//   - 等待期间 ctx 被取消：不取走任何元素，返回 ctx.Err()。
func (q *Queue[T]) Recv(ctx context.Context) (T, error) {
	var zero T
	for {
		q.mu.Lock()
		if q.size > 0 {
			v := q.buf[q.head]
			q.buf[q.head] = zero
			q.head = (q.head + 1) % len(q.buf)
			q.size--
			q.consumed++
			q.recordLocked(EvRecv, 1)
			q.broadcastLocked()
			q.mu.Unlock()
			return v, nil
		}
		switch q.st {
		case stateAborted:
			q.mu.Unlock()
			return zero, ErrAborted
		case stateClosing:
			q.mu.Unlock()
			return zero, ErrClosed
		}
		ch := q.change
		q.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			q.mu.Lock()
			q.recordLocked(EvRecvCancel, 1)
			q.mu.Unlock()
			return zero, ctx.Err()
		}
	}
}

// Close 发起正常关闭：不再接受新元素，已有元素留给消费者排空。
// 返回 true 表示本次调用生效；若故障关闭先生效（或已关闭过），返回 false，
// 队列语义由首次生效的关闭方式决定。
func (q *Queue[T]) Close() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.st != stateOpen {
		return false
	}
	q.st = stateClosing
	q.recordLocked(EvClose, 0)
	q.broadcastLocked()
	return true
}

// Abort 发起故障关闭：队列立即停止，缓冲中的剩余元素作为余项返回给调用者。
// 若正常关闭先生效（或已关闭过），返回 nil，本次调用不改变队列语义。
// 阻塞中的发送者与接收者会被唤醒并收到 ErrAborted，他们手中未入队的元素不受影响。
func (q *Queue[T]) Abort() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.st != stateOpen {
		return nil
	}
	q.st = stateAborted
	q.recordLocked(EvAbort, 0)
	n := q.size
	left := make([]T, 0, n)
	var zero T
	for i := 0; i < n; i++ {
		idx := (q.head + i) % len(q.buf)
		left = append(left, q.buf[idx])
		q.buf[idx] = zero
	}
	q.head = 0
	q.size = 0
	q.returned += uint64(n)
	if n > 0 {
		q.recordLocked(EvReturn, n)
	}
	q.broadcastLocked()
	return left
}

// History 返回操作历史的副本，按全局序号递增排列，可用 Replay 重放校验。
func (q *Queue[T]) History() []Event {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Event, len(q.hist))
	copy(out, q.hist)
	return out
}

// Conservation 是队列守恒不变量的快照。
type Conservation struct {
	Accepted uint64 // 已接收入队
	Consumed uint64 // 已消费出队
	Returned uint64 // 故障关闭返回的余项
	Buffered int    // 当前缓冲中
	Capacity int    // 容量上限
}

// Conservation 返回当前守恒快照。
func (q *Queue[T]) Conservation() Conservation {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Conservation{
		Accepted: q.accepted,
		Consumed: q.consumed,
		Returned: q.returned,
		Buffered: q.size,
		Capacity: len(q.buf),
	}
}

// OwnershipOK 检查所有权守恒：每个被接管的元素最终恰归于
// “已消费 / 已作为余项返回 / 仍在缓冲” 三者之一，不丢失、不重复。
func (c Conservation) OwnershipOK() bool {
	return c.Accepted == c.Consumed+c.Returned+uint64(c.Buffered)
}

// CapacityOK 检查容量守恒：缓冲占用始终在 [0, Capacity] 内。
func (c Conservation) CapacityOK() bool {
	return c.Buffered >= 0 && c.Buffered <= c.Capacity
}
