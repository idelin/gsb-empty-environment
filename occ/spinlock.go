package occ

import (
	"runtime"
	"sync/atomic"
)

// spinlock 是一个基于 CAS 的自旋锁，仅在提交阶段短暂持有。
// 读路径完全不使用它，因此读操作是无阻塞的。
type spinlock struct {
	flag int32
}

func (s *spinlock) Lock() {
	for !atomic.CompareAndSwapInt32(&s.flag, 0, 1) {
		runtime.Gosched()
	}
}

func (s *spinlock) Unlock() {
	atomic.StoreInt32(&s.flag, 0)
}
