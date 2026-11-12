//go:build linux && (amd64 || arm64)

package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// Linux ioctl request numbers, derived from _IOC() in asm-generic/ioctl.h and
// the UAPI struct sizes:
//
//	_IOWR('!', 0, struct seccomp_notif)      = 0xc0502100
//	_IOWR('!', 1, struct seccomp_notif_resp) = 0xc0182101
//	_IOW('!',  2, __u64)                     = 0x40082102
//	_IOW('!',  3, struct seccomp_notif_addfd) = 0x40182103
const (
	ioctlNotifRecv    uintptr = 0xc0502100
	ioctlNotifSend    uintptr = 0xc0182101
	ioctlNotifIDValid uintptr = 0x40082102
	ioctlNotifAddFD   uintptr = 0x40182103
)

const (
	addFDFlagSend  uint32  = 1 << 1 // SECCOMP_ADDFD_FLAG_SEND
	resolveNoMagic uint64  = 0x02   // RESOLVE_NO_MAGICLINKS
	resolveInRoot  uint64  = 0x10   // RESOLVE_IN_ROOT
	sysOpenat2     uintptr = 437
)

type seccompData struct {
	Nr                 int32
	Arch               uint32
	InstructionPointer uint64
	Args               [6]uint64
}

type seccompNotif struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Data  seccompData
}

type seccompNotifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

type seccompNotifAddfd struct {
	ID       uint64
	Flags    uint32
	SrcFD    uint32
	NewFD    uint32
	Reserved uint32
}

type openHow struct {
	Flags   uint64
	Mode    uint64
	Resolve uint64
}

type iovec struct {
	// Base is a uintptr rather than *byte so a remote-process address can be
	// stored without converting an arbitrary integer to unsafe.Pointer.
	Base uintptr
	Len  uint64
}

// The ioctl request numbers above encode these UAPI sizes. Fail the build if
// a struct layout ever ceases to match Linux's 64-bit ABI.
var (
	_ [80 - unsafe.Sizeof(seccompNotif{})]byte
	_ [unsafe.Sizeof(seccompNotif{}) - 80]byte
	_ [24 - unsafe.Sizeof(seccompNotifResp{})]byte
	_ [unsafe.Sizeof(seccompNotifResp{}) - 24]byte
	_ [24 - unsafe.Sizeof(seccompNotifAddfd{})]byte
	_ [unsafe.Sizeof(seccompNotifAddfd{}) - 24]byte
	_ [64 - unsafe.Sizeof(seccompData{})]byte
	_ [unsafe.Sizeof(seccompData{}) - 64]byte
	_ [24 - unsafe.Sizeof(openHow{})]byte
	_ [unsafe.Sizeof(openHow{}) - 24]byte
	_ [16 - unsafe.Sizeof(iovec{})]byte
	_ [unsafe.Sizeof(iovec{}) - 16]byte
)

// LinuxKernel implements Kernel with real Linux seccomp user-notification
// ioctls and process_vm_readv. listenerFD is the fd returned by
// SECCOMP_SET_MODE_FILTER with SECCOMP_FILTER_FLAG_NEW_LISTENER.
type LinuxKernel struct {
	listenerFD int
}

var _ Kernel = (*LinuxKernel)(nil)

// NewLinuxKernel returns a LinuxKernel for the given listener fd.
func NewLinuxKernel(listenerFD int) *LinuxKernel {
	return &LinuxKernel{listenerFD: listenerFD}
}

// Receive blocks on SECCOMP_IOCTL_NOTIF_RECV. It is provided as a convenience
// for supervisors; Handler does not require it.
func (k *LinuxKernel) Receive() (Notification, error) {
	var raw seccompNotif
	if err := k.ioctl(ioctlNotifRecv, unsafe.Pointer(&raw)); err != nil {
		return Notification{}, fmt.Errorf("SECCOMP_IOCTL_NOTIF_RECV: %w", err)
	}
	return Notification{
		ID:                 raw.ID,
		PID:                raw.PID,
		Flags:              raw.Flags,
		Syscall:            uint32(raw.Data.Nr),
		Arch:               raw.Data.Arch,
		InstructionPointer: raw.Data.InstructionPointer,
		Args:               raw.Data.Args,
	}, nil
}

// NotifValid implements SECCOMP_IOCTL_NOTIF_ID_VALID.
func (k *LinuxKernel) NotifValid(id uint64) error {
	if err := k.ioctl(ioctlNotifIDValid, unsafe.Pointer(&id)); err != nil {
		if isGoneErrno(err) {
			return ErrNotificationGone
		}
		return fmt.Errorf("SECCOMP_IOCTL_NOTIF_ID_VALID: %w", err)
	}
	return nil
}

// ReadCString copies at most max bytes from the traced process in a single
// process_vm_readv call and returns the bytes before the first NUL.
//
// A short read is not retried: retrying would let the traced process change
// the pathname between two kernel copies of the same address range.
func (k *LinuxKernel) ReadCString(pid uint32, addr uint64, max int) ([]byte, error) {
	if pid == 0 {
		return nil, fmt.Errorf("invalid pid 0: %w", syscall.EINVAL)
	}
	if max <= 0 || max > MaxPathBytes {
		max = MaxPathBytes
	}
	if addr == 0 {
		return nil, fmt.Errorf("null pathname pointer: %w", syscall.EFAULT)
	}
	if addr > ^uint64(0)-uint64(max) {
		return nil, fmt.Errorf("pathname pointer overflows: %w", syscall.EFAULT)
	}

	buf := make([]byte, max)
	n, err := processVMReadv(int(pid), buf, addr)
	if err != nil {
		return nil, fmt.Errorf("process_vm_readv pid %d addr %#x: %w", pid, addr, err)
	}
	if n <= 0 {
		return nil, fmt.Errorf("process_vm_readv read 0 bytes: %w", syscall.EFAULT)
	}
	if n > len(buf) {
		return nil, fmt.Errorf("process_vm_readv returned %d > %d: %w", n, len(buf), syscall.EIO)
	}

	if nul := bytes.IndexByte(buf[:n], 0); nul >= 0 {
		return buf[:nul], nil
	}
	// No terminator in the one copy we are allowed to make. Do not read again.
	if n < len(buf) {
		return nil, fmt.Errorf("partial pathname read (%d bytes) without NUL: %w", n, syscall.EFAULT)
	}
	return nil, fmt.Errorf("pathname has no NUL within %d bytes: %w", max, syscall.ENAMETOOLONG)
}

// ProcessView returns /proc/<pid>/root as the root handle and the process's
// cwd relative to that root. Both symlink targets are evaluated in the
// supervisor's mount namespace and then checked for containment.
func (k *LinuxKernel) ProcessView(pid uint32) (View, error) {
	if pid == 0 {
		return View{}, fmt.Errorf("invalid pid 0: %w", syscall.EINVAL)
	}
	rootTarget, err := os.Readlink(fmt.Sprintf("/proc/%d/root", pid))
	if err != nil {
		return View{}, fmt.Errorf("read process root: %w", err)
	}
	cwdTarget, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return View{}, fmt.Errorf("read process cwd: %w", err)
	}
	cwdRel, err := relativeToRoot(rootTarget, cwdTarget)
	if err != nil {
		return View{}, fmt.Errorf("process cwd is outside process root: %w", err)
	}
	return View{
		Root: fmt.Sprintf("/proc/%d/root", pid),
		Cwd:  cwdRel,
	}, nil
}

// DirFDBase resolves a non-AT_FDCWD dirfd for openat into a path relative to
// the process root.
func (k *LinuxKernel) DirFDBase(pid uint32, dirfd int32) (string, error) {
	if pid == 0 {
		return "", fmt.Errorf("invalid pid 0: %w", syscall.EINVAL)
	}
	if dirfd < 0 {
		return "", fmt.Errorf("invalid dirfd %d: %w", dirfd, syscall.EINVAL)
	}
	fdTarget, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", pid, dirfd))
	if err != nil {
		return "", fmt.Errorf("read dirfd %d: %w", dirfd, err)
	}
	rootTarget, err := os.Readlink(fmt.Sprintf("/proc/%d/root", pid))
	if err != nil {
		return "", fmt.Errorf("read process root: %w", err)
	}
	base, err := relativeToRoot(rootTarget, fdTarget)
	if err != nil {
		return "", fmt.Errorf("dirfd %d is outside process root: %w", dirfd, err)
	}
	return base, nil
}

// OpenInView opens relPath relative to view.Root with openat2 RESOLVE_IN_ROOT
// and RESOLVE_NO_MAGICLINKS. The latter also prevents a /proc magic link in
// the attacker-controlled pathname from escaping the root handle.
func (k *LinuxKernel) OpenInView(view View, relPath string, flags uint32, mode uint32) (*os.File, error) {
	if view.Root == "" {
		return nil, fmt.Errorf("empty process root: %w", syscall.EINVAL)
	}
	if relPath == "" {
		return nil, fmt.Errorf("empty root-relative path: %w", syscall.ENOENT)
	}

	root, err := os.Open(view.Root)
	if err != nil {
		return nil, fmt.Errorf("open process root %q: %w", view.Root, err)
	}
	defer root.Close()

	how := openHow{
		Flags:   uint64(flags),
		Mode:    uint64(mode),
		Resolve: resolveInRoot | resolveNoMagic,
	}
	fd, err := openat2(int(root.Fd()), relPath, how)
	if err != nil {
		return nil, fmt.Errorf("openat2 %q in %q: %w", relPath, view.Root, err)
	}
	syscall.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), relPath), nil
}

// SendFD installs src into the target and sends the notification response in
// one SECCOMP_IOCTL_NOTIF_ADDFD call with SECCOMP_ADDFD_FLAG_SEND. The
// kernel picks the target fd, matching openat(2)'s "lowest free fd" behaviour.
func (k *LinuxKernel) SendFD(id uint64, src *os.File) (int, error) {
	if src == nil {
		return -1, fmt.Errorf("nil source fd: %w", syscall.EBADF)
	}
	addfd := seccompNotifAddfd{
		ID:    id,
		Flags: addFDFlagSend,
		SrcFD: uint32(src.Fd()),
	}
	r1, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(k.listenerFD),
		ioctlNotifAddFD,
		uintptr(unsafe.Pointer(&addfd)),
	)
	if errno != 0 {
		if isGoneErrno(errno) {
			return -1, ErrNotificationGone
		}
		return -1, fmt.Errorf("SECCOMP_IOCTL_NOTIF_ADDFD: %w", errno)
	}
	return int(r1), nil
}

// Respond sends an error response. For success responses the handler uses
// SendFD instead, so the response carries the injected descriptor.
func (k *LinuxKernel) Respond(id uint64, val int32, errno syscall.Errno) error {
	resp := seccompNotifResp{
		ID:    id,
		Val:   int64(val),
		Error: -int32(errno),
	}
	if err := k.ioctl(ioctlNotifSend, unsafe.Pointer(&resp)); err != nil {
		if isGoneErrno(err) {
			return ErrNotificationGone
		}
		return fmt.Errorf("SECCOMP_IOCTL_NOTIF_SEND: %w", err)
	}
	return nil
}

func (k *LinuxKernel) ioctl(req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(k.listenerFD),
		req,
		uintptr(arg),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func isGoneErrno(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ESRCH)
}

func processVMReadv(pid int, dst []byte, remote uint64) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	local := iovec{Base: uintptr(unsafe.Pointer(&dst[0])), Len: uint64(len(dst))}
	rem := iovec{Base: uintptr(remote), Len: uint64(len(dst))}
	r1, _, errno := syscall.Syscall6(
		sysProcessVMReadv,
		uintptr(pid),
		uintptr(unsafe.Pointer(&local)),
		1,
		uintptr(unsafe.Pointer(&rem)),
		1,
		0,
	)
	runtime.KeepAlive(dst)
	if errno != 0 {
		return 0, errno
	}
	return int(r1), nil
}

func openat2(dirfd int, pathname string, how openHow) (int, error) {
	if pathname == "" {
		return -1, syscall.ENOENT
	}
	p, err := syscall.BytePtrFromString(pathname)
	if err != nil {
		return -1, err
	}
	r1, _, errno := syscall.Syscall6(
		sysOpenat2,
		uintptr(dirfd),
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&how)),
		unsafe.Sizeof(how),
		0,
		0,
	)
	runtime.KeepAlive(p)
	if errno != 0 {
		return -1, errno
	}
	return int(r1), nil
}

func relativeToRoot(rootTarget, target string) (string, error) {
	root := path.Clean(rootTarget)
	target = path.Clean(target)
	if root == "" {
		return "", fmt.Errorf("empty root target: %w", syscall.EINVAL)
	}
	if root == "/" {
		return strings.TrimPrefix(target, "/"), nil
	}
	if target == root {
		return "", nil
	}
	prefix := root + "/"
	if strings.HasPrefix(target, prefix) {
		return strings.TrimPrefix(target, prefix), nil
	}
	return "", syscall.EXDEV
}
