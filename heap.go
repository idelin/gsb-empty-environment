package ttlru

import (
	"container/list"
	"time"
)

// entry 是缓存中的最小单元，同时挂在两条索引上：
//  1. LRU 双向链表（container/list）
//  2. 按到期时间排序的小顶堆（expireHeap）
type entry struct {
	key        string
	value      interface{}
	expireAt   time.Time     // 绝对过期时间（TTD），零值表示永不过期
	idle       time.Duration // 滑动过期窗口（TTI），<=0 表示不启用
	lastAccess time.Time     // 最近一次访问时间，用于计算滑动到期点
	elem       *list.Element // 在 LRU 链表中的位置
	heapIndex  int           // 在小顶堆中的下标，-1 表示不在堆中
}

// deadline 返回该条目生效的到期时间：绝对过期与滑动过期取较早者。
// 零值表示永不过期。
func (e *entry) deadline() time.Time {
	d := e.expireAt
	if e.idle > 0 {
		idleDeadline := e.lastAccess.Add(e.idle)
		if d.IsZero() || idleDeadline.Before(d) {
			d = idleDeadline
		}
	}
	return d
}

// expired 判断条目在 now 时刻是否已过期。
func (e *entry) expired(now time.Time) bool {
	d := e.deadline()
	return !d.IsZero() && !now.Before(d)
}

// expireHeap 是按 deadline 排序的小顶堆，堆顶永远是“最早到期”的条目，
// 因此批量回收时只需从堆顶连续弹出已过期元素即可。
type expireHeap []*entry

func (h expireHeap) Len() int { return len(h) }

func (h expireHeap) Less(i, j int) bool {
	di, dj := h[i].deadline(), h[j].deadline()
	switch {
	case di.IsZero():
		return false // 永不过期的条目沉底
	case dj.IsZero():
		return true
	default:
		return di.Before(dj)
	}
}

func (h expireHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *expireHeap) Push(x interface{}) {
	e := x.(*entry)
	e.heapIndex = len(*h)
	*h = append(*h, e)
}

func (h *expireHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil // 释放引用，便于 GC
	e.heapIndex = -1
	*h = old[:n-1]
	return e
}
