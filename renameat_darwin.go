//go:build darwin

package netsec

import (
	"syscall"
	"unsafe"
)

// renameat(2) is not exported by the syscall package on Darwin. The syscall
// number is stable across Darwin architectures (x86_64 and arm64).
const sysRenameat = 465

func renameInDir(oldDir *dirHandle, oldName string, newDir *dirHandle, newName string) error {
	oldPath, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPath, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		sysRenameat,
		uintptr(oldDir.file.Fd()),
		uintptr(unsafe.Pointer(oldPath)),
		uintptr(newDir.file.Fd()),
		uintptr(unsafe.Pointer(newPath)),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
