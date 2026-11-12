//go:build cgo

package netsec

import (
	"runtime"
	"testing"
	"unsafe"
)

// 真实 cgo 链路：调用前检查通过 → C 回调保存 Go 指针 →
// 返回后持有表报告违规 → C 释放后违规消失。
func TestCCallbackRetainsPointer(t *testing.T) {
	chk := NewChecker()
	cgoHook = chk
	defer func() { cgoHook = nil }()

	buf := make([]byte, 32)
	chk.RegisterGo("buf", buf)

	if err := chk.CheckArg(unsafe.Pointer(&buf[0])); err != nil {
		t.Fatalf("纯数据缓冲区应通过调用前检查: %v", err)
	}

	cgoStorePointer(unsafe.Pointer(&buf[0])) // C 侧保存 Go 指针

	if errs := chk.CheckRetained(); len(errs) != 1 {
		t.Fatalf("期望 1 个保留违规，实际 %d: %v", len(errs), errs)
	}

	cgoDropPointer() // C 侧释放
	if errs := chk.CheckRetained(); len(errs) != 0 {
		t.Fatalf("C 释放后不应再有违规: %v", errs)
	}
	runtime.KeepAlive(buf)
}

// 真实 C.malloc 块：登记为 C 块后，只含 C 指针的结构通过检查。
func TestCAllocatedBlock(t *testing.T) {
	chk := NewChecker()
	blk := cgoAlloc(48)
	defer cgoFree(blk)
	chk.RegisterCBlock(blk, 48, "C.malloc")

	type CNode struct {
		Buf unsafe.Pointer
		Out unsafe.Pointer
		Cap uint64
	}
	node := &CNode{
		Buf: blk,
		Out: unsafe.Pointer(uintptr(blk) + 16),
		Cap: 48,
	}
	chk.RegisterGo("cnode", node)

	if err := chk.CheckArg(unsafe.Pointer(node)); err != nil {
		t.Fatalf("只含 C 指针的结构应通过: %v", err)
	}
	if err := chk.CheckArg(blk); err != nil {
		t.Fatalf("C 块指针应直接通过: %v", err)
	}
}
