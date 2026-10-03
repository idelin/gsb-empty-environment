package paxos

import "sync/atomic"

// slotState 是单个日志槽位的不可变快照。
// 所有更新都通过整体替换 + CAS 完成，读取永远不需要加锁。
type slotState struct {
	acceptedBallot Ballot // 最近一次 Accept 的提案号
	value          []byte // 已接受的值（调用方不得修改）
	hasValue       bool   // 是否已有 Accepted 值
	committed      bool   // 是否已提交（提交后值不可再变）
}

// slotLog 是无锁的可增长日志槽数组。
// 数组扩容通过 copy-on-extend + CAS 完成；槽位更新通过指针 CAS 完成。
// 整个结构没有任何互斥锁。
type slotLog struct {
	arr atomic.Pointer[[]*atomic.Pointer[slotState]]
}

func newSlotLog() *slotLog {
	l := &slotLog{}
	initial := make([]*atomic.Pointer[slotState], 8)
	for i := range initial {
		initial[i] = &atomic.Pointer[slotState]{}
	}
	l.arr.Store(&initial)
	return l
}

// slot 返回 idx 处的槽位指针，必要时无锁扩容。
func (l *slotLog) slot(idx uint64) *atomic.Pointer[slotState] {
	for {
		a := l.arr.Load()
		if int(idx) < len(*a) {
			return (*a)[idx]
		}
		l.grow(idx)
	}
}

// grow 把底层数组扩容到能容纳 idx，CAS 失败则重试（失败者直接复用胜者结果）。
func (l *slotLog) grow(idx uint64) {
	for {
		a := l.arr.Load()
		oldLen := len(*a)
		if int(idx) < oldLen {
			return
		}
		newLen := oldLen * 2
		for newLen <= int(idx) {
			newLen *= 2
		}
		na := make([]*atomic.Pointer[slotState], newLen)
		copy(na, *a)
		for i := oldLen; i < newLen; i++ {
			na[i] = &atomic.Pointer[slotState]{}
		}
		if l.arr.CompareAndSwap(a, &na) {
			return
		}
	}
}

// maxIndex 返回已分配且非空的最高槽位下标；日志为空时返回 -1。
func (l *slotLog) maxIndex() int64 {
	a := l.arr.Load()
	for i := len(*a) - 1; i >= 0; i-- {
		if (*a)[i].Load() != nil {
			return int64(i)
		}
	}
	return -1
}

// load 读取 idx 处槽位快照，未分配时返回 nil。
func (l *slotLog) load(idx uint64) *slotState {
	a := l.arr.Load()
	if int(idx) >= len(*a) {
		return nil
	}
	return (*a)[idx].Load()
}
