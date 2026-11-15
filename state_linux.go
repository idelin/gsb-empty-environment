//go:build linux

package netsec

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	fsIOCGetVersion = uintptr(0x80000000) |
		(uintptr(unsafe.Sizeof(uintptr(0))) << 16) |
		(uintptr('v') << 8) |
		1
)

func readState(path string) (FileState, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return FileState{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return FileState{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileState{}, errors.New("unexpected stat representation")
	}

	generation, err := inodeGeneration(file)
	if err != nil {
		return FileState{}, err
	}
	ima, err := readXattr(file, XAttrIMA, false)
	if err != nil {
		return FileState{}, err
	}
	selinux, err := readXattr(file, XAttrSELinux, false)
	if err != nil {
		return FileState{}, err
	}
	evm, err := readXattr(file, XAttrEVM, true)
	if err != nil {
		return FileState{}, err
	}

	return FileState{
		Path:       path,
		IMA:        ima,
		SELinux:    selinux,
		Inode:      stat.Ino,
		Generation: generation,
		UID:        stat.Uid,
		GID:        stat.Gid,
		Mode:       stat.Mode,
		EVM:        evm,
	}, nil
}

func readXattr(file *os.File, name string, required bool) ([]byte, error) {
	namePtr, err := syscall.BytePtrFromString(name)
	if err != nil {
		return nil, err
	}

	size, _, errno := syscall.Syscall6(
		syscall.SYS_FGETXATTR,
		file.Fd(),
		uintptr(unsafe.Pointer(namePtr)),
		0,
		0,
		0,
		0,
	)
	if errno != 0 {
		if errno == syscall.ENODATA {
			if required {
				return nil, ErrMissingEVM
			}
			return nil, nil
		}
		return nil, fmt.Errorf("fgetxattr %s: %w", name, errno)
	}
	if size == 0 {
		return []byte{}, nil
	}

	value := make([]byte, size)
	n, _, errno := syscall.Syscall6(
		syscall.SYS_FGETXATTR,
		file.Fd(),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(&value[0])),
		size,
		0,
		0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("fgetxattr %s: %w", name, errno)
	}
	return value[:n], nil
}

func inodeGeneration(file *os.File) (uint64, error) {
	var generation uintptr
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		file.Fd(),
		fsIOCGetVersion,
		uintptr(unsafe.Pointer(&generation)),
	)
	if errno != 0 {
		return 0, fmt.Errorf("%w: %v", ErrGenerationUnavail, errno)
	}
	return uint64(generation), nil
}
