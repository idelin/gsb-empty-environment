//go:build darwin && cgo

package netsec

/*
#include <sys/types.h>
#include <sys/xattr.h>
#include <errno.h>
#include <stdlib.h>

static int ima_read_xattr(const char *path, const char *name, void *buf, size_t size, ssize_t *out) {
	ssize_t n = getxattr(path, name, buf, size, 0, 0);
	*out = n;
	if (n < 0) {
		if (errno == ENOATTR) {
			return 1;
		}
		return 2 + errno;
	}
	return 0;
}

static int ima_write_xattr(const char *path, const char *name, const void *buf, size_t size) {
	int rc = setxattr(path, name, buf, size, 0, 0);
	if (rc != 0) {
		return 2 + errno;
	}
	return 0;
}

static int ima_remove_xattr(const char *path, const char *name) {
	int rc = removexattr(path, name, 0);
	if (rc != 0) {
		if (errno == ENOATTR) {
			return 1;
		}
		return 2 + errno;
	}
	return 0;
}
*/
import "C"

import (
	"fmt"
	"syscall"
	"unsafe"
)

func xattrCode(code C.int) error {
	switch int(code) {
	case 0:
		return nil
	case 1:
		return ErrAttributeMissing
	default:
		return fmt.Errorf("xattr: %w", syscall.Errno(int(code)-2))
	}
}

func platformReadXattr(path, name string) ([]byte, error) {
	cpath := C.CString(path)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cpath))
	defer C.free(unsafe.Pointer(cname))

	var size C.ssize_t
	code := C.ima_read_xattr(cpath, cname, nil, 0, &size)
	if err := xattrCode(code); err != nil {
		return nil, err
	}
	if size == 0 {
		return []byte{}, nil
	}

	buf := make([]byte, int(size))
	var written C.ssize_t
	code = C.ima_read_xattr(cpath, cname, unsafe.Pointer(&buf[0]), C.size_t(len(buf)), &written)
	if err := xattrCode(code); err != nil {
		return nil, err
	}
	if written < 0 || int(written) > len(buf) {
		return nil, ErrLengthMismatch
	}
	return buf[:int(written)], nil
}

func platformSetXattr(path, name string, data []byte) error {
	cpath := C.CString(path)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cpath))
	defer C.free(unsafe.Pointer(cname))

	var ptr unsafe.Pointer
	if len(data) != 0 {
		ptr = unsafe.Pointer(&data[0])
	}
	code := C.ima_write_xattr(cpath, cname, ptr, C.size_t(len(data)))
	return xattrCode(code)
}

func platformRemoveXattr(path, name string) error {
	cpath := C.CString(path)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cpath))
	defer C.free(unsafe.Pointer(cname))
	return xattrCode(C.ima_remove_xattr(cpath, cname))
}
