package occ

import (
	"sync/atomic"
	"unsafe"
)

// atomicRecord 用 unsafe.Pointer 做无锁原子替换（兼容旧版 Go）。
type atomicRecord struct {
	p unsafe.Pointer
}

// load 原子读取当前记录（无锁）。
func (s *atomicRecord) load() *record {
	return (*record)(atomic.LoadPointer(&s.p))
}

// store 原子发布一条新记录。调用方必须持有提交锁或处于初始化阶段。
func (s *atomicRecord) store(r *record) {
	atomic.StorePointer(&s.p, unsafe.Pointer(r))
}
