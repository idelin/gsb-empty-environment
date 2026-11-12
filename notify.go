package netsec

import (
	"errors"
	"fmt"
	"runtime"
)

const (
	// AT_FDCWD is the openat(2) sentinel meaning "relative to the process cwd".
	AT_FDCWD int32 = -100

	// MaxPathBytes is the maximum number of bytes copied from the traced
	// process for a pathname. Linux PATH_MAX is 4096 bytes including the NUL.
	MaxPathBytes = 4096
)

// Audit architecture values used in struct seccomp_data. They are stable
// values from linux/audit.h.
const (
	AuditArchX86_64  uint32 = 0xC000003E
	AuditArchAArch64 uint32 = 0xC00000B7
	AuditArchI386    uint32 = 0x40000003
	AuditArchARM     uint32 = 0x40000028
	AuditArchRISCV64 uint32 = 0xC00000F3
)

var (
	// ErrNotificationGone means the notification ID is no longer valid. No
	// response should be sent in this case; the target syscall has already
	// been interrupted or the target has exited.
	ErrNotificationGone = errors.New("seccomp user notification is no longer valid")

	// ErrUnsupportedSyscall means the notification is not an openat for an
	// architecture this package knows about.
	ErrUnsupportedSyscall = errors.New("unsupported notification syscall")

	// ErrNoKernel is returned when a Handler has no Kernel backend.
	ErrNoKernel = errors.New("netsec: no notification kernel configured")
)

// Notification is the part of struct seccomp_notif that a supervisor needs.
// The kernel fills it in when it blocks the traced task.
type Notification struct {
	ID                 uint64
	PID                uint32
	Flags              uint32
	Syscall            uint32
	Arch               uint32
	InstructionPointer uint64
	Args               [6]uint64
}

// OpenRequest is an openat(2) request decoded from a Notification. PathAddr
// is an address in the traced process, not a supervisor pointer.
type OpenRequest struct {
	DirFD    int32
	PathAddr uint64
	Flags    uint32
	Mode     uint32
}

// OpenatSyscall returns the openat(2) syscall number for an audit
// architecture. An arch of zero selects the architecture this binary was
// built for; this is useful for tests and for supervisors that only ever see
// native tasks.
func OpenatSyscall(arch uint32) (uint32, bool) {
	if arch == 0 {
		switch runtime.GOARCH {
		case "amd64":
			arch = AuditArchX86_64
		case "arm64":
			arch = AuditArchAArch64
		case "386":
			arch = AuditArchI386
		case "arm":
			arch = AuditArchARM
		case "riscv64":
			arch = AuditArchRISCV64
		default:
			return 0, false
		}
	}

	switch arch {
	case AuditArchX86_64:
		return 257, true
	case AuditArchAArch64, AuditArchRISCV64:
		return 56, true
	case AuditArchI386:
		return 295, true
	case AuditArchARM:
		return 322, true
	default:
		return 0, false
	}
}

// OpenatRequest decodes the openat arguments from a notification. The
// supervisor must not dereference Args[1] itself; the address is handed to
// Kernel.ReadCString, which performs the one permitted copy.
func (n Notification) OpenatRequest() (OpenRequest, error) {
	nr, ok := OpenatSyscall(n.Arch)
	if !ok {
		return OpenRequest{}, fmt.Errorf("%w: audit arch %#x", ErrUnsupportedSyscall, n.Arch)
	}
	if n.Syscall != nr {
		return OpenRequest{}, fmt.Errorf("%w: syscall %d (want openat %d)", ErrUnsupportedSyscall, n.Syscall, nr)
	}
	return OpenRequest{
		DirFD:    int32(n.Args[0]),
		PathAddr: n.Args[1],
		Flags:    uint32(n.Args[2]),
		Mode:     uint32(n.Args[3]),
	}, nil
}
