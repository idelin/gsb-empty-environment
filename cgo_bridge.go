//go:build cgo

package netsec

/*
#include <stdlib.h>

extern void netsecTestRetain(void *p);
extern void netsecTestRelease(void *p);

static void *savedPtr;

// 模拟违规的 C API：把传入的 Go 指针保存到 C 全局变量，
// 并经回调登记到检查器的 C 端持有表。
static void cStorePointer(void *p) {
	savedPtr = p;
	netsecTestRetain(p);
}

// 模拟 C API 释放此前保存的指针。
static void cDropPointer(void) {
	void *p = savedPtr;
	savedPtr = 0;
	netsecTestRelease(p);
}

static void *cAlloc(size_t n) { return malloc(n); }
static void cFree(void *p) { free(p); }
*/
import "C"
import "unsafe"

func cgoStorePointer(p unsafe.Pointer) { C.cStorePointer(p) }

func cgoDropPointer() { C.cDropPointer() }

func cgoAlloc(n uintptr) unsafe.Pointer { return C.cAlloc(C.size_t(n)) }

func cgoFree(p unsafe.Pointer) { C.cFree(p) }
