//go:build cgo

package netsec

import "C"
import "unsafe"

// cgoHook 由测试安装，把 C 回调接入 Checker 的持有表。
var cgoHook *Checker

//export netsecTestRetain
func netsecTestRetain(p unsafe.Pointer) {
	if cgoHook != nil {
		cgoHook.CRetain(p, "C 回调保存")
	}
}

//export netsecTestRelease
func netsecTestRelease(p unsafe.Pointer) {
	if cgoHook != nil {
		cgoHook.CRelease(p)
	}
}
