// Package lockfree 提供基于 CAS 的无锁并发数据结构。
//
// Queue 是 Michael & Scott 无锁队列的 Go 实现：
// 所有并发控制均通过 atomic.Pointer 的 Compare-And-Swap 完成，
// 不持有任何互斥锁，Enqueue/Dequeue 均为非阻塞（lock-free）操作。
package lockfree

import "sync/atomic"

// node 是队列内部节点。next 只会被 CAS 从 nil 推进到后继节点。
type node[T any] struct {
	value T
	next  atomic.Pointer[node[T]]
}

// Queue 是一个多生产者多消费者（MPMC）无锁 FIFO 队列。
// 零值不可用，请使用 NewQueue 构造。
type Queue[T any] struct {
	head  atomic.Pointer[node[T]] // 哨兵节点，出队端
	tail  atomic.Pointer[node[T]] // 入队端
	count atomic.Int64            // 近似长度（并发下为最终一致）
}

// NewQueue 创建一个空队列，初始时 head==tail 指向同一个哨兵节点。
func NewQueue[T any]() *Queue[T] {
	dummy := &node[T]{}
	q := &Queue[T]{}
	q.head.Store(dummy)
	q.tail.Store(dummy)
	return q
}

// Enqueue 将元素追加到队尾。全程 CAS 重试，无锁、无阻塞。
func (q *Queue[T]) Enqueue(v T) {
	n := &node[T]{value: v}
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail != q.tail.Load() { // tail 已被其他线程推进，重读
			continue
		}
		if next == nil {
			// 尝试把新节点链接到尾节点之后
			if tail.next.CompareAndSwap(nil, n) {
				// 链接成功后推进 tail；失败也无妨，其他线程会帮忙推进
				q.tail.CompareAndSwap(tail, n)
				q.count.Add(1)
				return
			}
		} else {
			// tail 落后了，帮忙推进后再试
			q.tail.CompareAndSwap(tail, next)
		}
	}
}

// Dequeue 从队头取出元素。队列为空时返回 (零值, false)，绝不阻塞。
func (q *Queue[T]) Dequeue() (T, bool) {
	for {
		head := q.head.Load()
		tail := q.tail.Load()
		next := head.next.Load()
		if head != q.head.Load() { // head 已移动，重读
			continue
		}
		if head == tail {
			if next == nil { // 队列确实为空
				var zero T
				return zero, false
			}
			// tail 落后于 head，帮忙推进 tail
			q.tail.CompareAndSwap(tail, next)
			continue
		}
		// 在 CAS 之前读取值，保证读到的是 next 成为新哨兵前的有效载荷
		v := next.value
		if q.head.CompareAndSwap(head, next) {
			q.count.Add(-1)
			return v, true
		}
	}
}

// Len 返回队列近似长度。并发读写时该值是最终一致的；
// 当所有生产者/消费者静止后，它精确等于队列中的元素个数。
func (q *Queue[T]) Len() int64 {
	return q.count.Load()
}

// Empty 报告队列在调用瞬间是否为空。
func (q *Queue[T]) Empty() bool {
	head := q.head.Load()
	return head == q.tail.Load() && head.next.Load() == nil
}
