//go:build darwin

package rootedpublish

import (
	"errors"
	"io"
	"syscall"
	"unsafe"
)

// Darwin does not expose the *at(2) family through the Go syscall package, so
// the syscall numbers are declared here and invoked through syscall.Syscall6.
// The arm64 ("arm64e") syscall table differs from the legacy x86_64 table; the
// values below are the stable arm64 numbers (XNU syscall_sw.h).
const (
	sysOpenAT      = 463
	sysFstatAT     = 470
	sysMkdirAT     = 475
	sysUnlinkAT    = 472
	sysRenameAT    = 465
	sysRenameATxNP = 488
)

// *at(2) flags (sys/fcntl.h on macOS).
const (
	atSYMLINKNOFOLLOW = 0x0020
)

// renamex_np flags (sys/stdio.h).
const (
	renameFlagExchange = 0x00000002
)

// openAT opens name relative to dirFD without ever resolving through a
// directory that the caller did not open first.
func openAT(dirFD int, name string, flags int, mode uint32) (int, error) {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return -1, err
	}
	r1, _, errno := syscall.Syscall6(sysOpenAT,
		uintptr(dirFD), uintptr(unsafe.Pointer(p)),
		uintptr(flags), uintptr(mode), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(r1), nil
}

// writeFD writes p to fd at its current offset (write(2)), retrying on EINTR
// and short writes. It avoids wrapping the descriptor in an *os.File, whose
// runtime finalizer could otherwise close it out from under the publisher.
func writeFD(fd int, p []byte) error {
	for len(p) > 0 {
		n, err := syscall.Write(fd, p)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// fstatAT stats name relative to dirFD. With atSYMLINKNOFOLLOW it stats the
// link itself rather than its target.
func fstatAT(dirFD int, name string, stat *syscall.Stat_t, flags int) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysFstatAT,
		uintptr(dirFD), uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(stat)), uintptr(flags), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// mkdirAT creates name relative to dirFD.
func mkdirAT(dirFD int, name string, mode uint32) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysMkdirAT,
		uintptr(dirFD), uintptr(unsafe.Pointer(p)),
		uintptr(mode), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// unlinkAT removes the directory entry name relative to dirFD without
// following a symlink.
func unlinkAT(dirFD int, name string, flags int) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysUnlinkAT,
		uintptr(dirFD), uintptr(unsafe.Pointer(p)),
		uintptr(flags), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// renameAT renames oldName (relative to oldDirFD) to newName (relative to
// newDirFD). It is atomic with respect to other processes.
func renameAT(oldDirFD int, oldName string, newDirFD int, newName string) error {
	oldp, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newp, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysRenameAT,
		uintptr(oldDirFD), uintptr(unsafe.Pointer(oldp)),
		uintptr(newDirFD), uintptr(unsafe.Pointer(newp)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// renameExchange atomically swaps two directory entries within the same
// directory (renamex_np with RENAME_EXCHANGE). Both entries must exist.
func renameExchange(dirFD int, a, b string) error {
	pa, err := syscall.BytePtrFromString(a)
	if err != nil {
		return err
	}
	pb, err := syscall.BytePtrFromString(b)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysRenameATxNP,
		uintptr(dirFD), uintptr(unsafe.Pointer(pa)),
		uintptr(dirFD), uintptr(unsafe.Pointer(pb)),
		uintptr(renameFlagExchange), 0)
	if errno != 0 {
		return errno
	}
	return nil
}
