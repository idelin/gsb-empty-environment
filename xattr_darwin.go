//go:build darwin

package netsec

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

func platformGetXAttr(path, name string, dest []byte) (int, error) {
	pathPtr, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0, err
	}
	namePtr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}

	var value unsafe.Pointer
	if len(dest) > 0 {
		value = unsafe.Pointer(&dest[0])
	}
	r0, _, errno := syscall.Syscall6(
		syscall.SYS_GETXATTR,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(value),
		uintptr(len(dest)),
		0,
		0,
	)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(namePtr)
	runtime.KeepAlive(dest)
	if errno != 0 {
		return int(r0), errno
	}
	return int(r0), nil
}

func platformSetXAttr(path, name string, data []byte, flags int) error {
	pathPtr, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	namePtr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}

	var value unsafe.Pointer
	if len(data) > 0 {
		value = unsafe.Pointer(&data[0])
	}
	_, _, errno := syscall.Syscall6(
		syscall.SYS_SETXATTR,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(value),
		uintptr(len(data)),
		0,
		uintptr(flags),
	)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(namePtr)
	runtime.KeepAlive(data)
	if errno != 0 {
		return errno
	}
	return nil
}

func platformRemoveXAttr(path, name string) error {
	pathPtr, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	namePtr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}

	_, _, errno := syscall.Syscall6(
		syscall.SYS_REMOVEXATTR,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(namePtr)),
		0,
		0,
		0,
		0,
	)
	runtime.KeepAlive(pathPtr)
	runtime.KeepAlive(namePtr)
	if errno != 0 {
		return errno
	}
	return nil
}

func isNoAttr(err error) bool {
	return errors.Is(err, syscall.ENOATTR)
}
