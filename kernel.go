package netsec

import (
	"os"
	"syscall"
)

// Kernel is the privileged part of the supervisor. The interface makes the
// security-relevant ordering testable and keeps all seccomp/ioctl specifics
// in LinuxKernel.
//
// Implementations must satisfy these contracts:
//
//   - ReadCString performs exactly one copy from pid's address space. It must
//     not retry a short read or re-read the same address; if the NUL is not
//     present in that copy, it returns an error.
//   - ProcessView and DirFDBase describe the traced process's filesystem view,
//     not the supervisor's.
//   - OpenInView opens relPath (which ResolvePath produced) relative to
//     view.Root and must confine symlink and ".." resolution to that root.
//     A plain filepath.Join followed by os.Open is not sufficient.
//   - SendFD installs src into the target and sends the notification response
//     containing the resulting target descriptor in one atomic operation
//     (SECCOMP_IOCTL_NOTIF_ADDFD with SECCOMP_ADDFD_FLAG_SEND).
//   - Respond sends an errno response; errno is positive in this API.
type Kernel interface {
	NotifValid(id uint64) error
	ReadCString(pid uint32, addr uint64, max int) ([]byte, error)
	ProcessView(pid uint32) (View, error)
	DirFDBase(pid uint32, dirfd int32) (string, error)
	OpenInView(view View, relPath string, flags uint32, mode uint32) (*os.File, error)
	SendFD(id uint64, src *os.File) (targetFD int, err error)
	Respond(id uint64, val int32, errno syscall.Errno) error
}
