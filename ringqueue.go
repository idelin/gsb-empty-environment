package coroutine

import "sync/atomic"

// ringSlot 环形队列的一个槽位。seq 是每槽位的序列号：
// 初始 seq == 下标；生产者看到 seq == pos 时可写，写后置为 pos+1；
// 消费者看到 seq == pos+1 时可读，读后置为 pos+capacity。
type ringSlot struct {
	seq atomic.Uint64
	val any
}

// RingQueue 是无锁（lock-free）有界 MPMC 环形队列，
// 基于 Dmitry Vyukov 的 bounded MPMC queue 算法：
// 头尾指针与每槽位序列号均通过 CAS 推进，全程无互斥锁。
// 容量自动向上取整为 2 的幂。多生产者多消费者并发安全。
type RingQueue struct {
	enq  atomic.Uint64 // 生产者位置（单调递增）
	pad0 [7]uint64     // 避免与 deq 伪共享
	deq  atomic.Uint64 // 消费者位置（单调递增）
	pad1 [7]uint64
	mask uint64
	buf  []ringSlot
}

// NewRingQueue 创建容量至少为 capacity 的无锁环形队列（实际容量向上取整为 2 的幂）。
func NewRingQueue(capacity int) *RingQueue {
	if capacity < 1 {
		capacity = 1
	}
	size := 1
	for size < capacity {
		size <<= 1
	}
	q := &RingQueue{
		mask: uint64(size - 1),
		buf:  make([]ringSlot, size),
	}
	for i := range q.buf {
		q.buf[i].seq.Store(uint64(i))
	}
	return q
}

// Cap 返回队列实际容量（2 的幂）。
func (q *RingQueue) Cap() int { return len(q.buf) }

// Enqueue 尝试入队。队列已满时立即返回 false（非阻塞）。
func (q *RingQueue) Enqueue(v any) bool {
	for {
		pos := q.enq.Load()
		slot := &q.buf[pos&q.mask]
		seq := slot.seq.Load()
		switch diff := int64(seq) - int64(pos); {
		case diff == 0: // 槽位空闲，尝试认领
			if q.enq.CompareAndSwap(pos, pos+1) {
				slot.val = v
				slot.seq.Store(pos + 1) // 发布：对消费者可见
				return true
			}
		case diff < 0: // 队列已满
			return false
		}
		// diff > 0：位置被其他生产者抢占，重试
	}
}

// Dequeue 尝试出队。队列为空时立即返回 (nil, false)（非阻塞）。
func (q *RingQueue) Dequeue() (any, bool) {
	for {
		pos := q.deq.Load()
		slot := &q.buf[pos&q.mask]
		seq := slot.seq.Load()
		switch diff := int64(seq) - int64(pos+1); {
		case diff == 0: // 槽位有数据，尝试认领
			if q.deq.CompareAndSwap(pos, pos+1) {
				v := slot.val
				slot.val = nil
				slot.seq.Store(pos + q.mask + 1) // 释放：对生产者可见
				return v, true
			}
		case diff < 0: // 队列为空
			return nil, false
		}
	}
}
