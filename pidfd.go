// Package netsec provides authorization logic for intercepting the
// pidfd_send_signal(2) system call in a Go sandbox host.
//
// A pidfd pins a process by (pid, start-time) at creation time. When a
// signal is later requested through the pidfd, the target is re-resolved
// in the caller's PID namespace and the pinned start-time is compared
// against the live process, so a recycled pid can never receive a signal
// meant for a dead process. Credential (same-user or CAP_KILL) and
// siginfo (si_code / si_signo) checks mirror the Linux kernel rules.
package netsec

import (
	"sync"
	"syscall"
)

// Errnos returned by the authorization checks. They mirror the values the
// real pidfd_send_signal(2) syscall would produce so the interceptor can
// relay them to the sandboxed caller unchanged.
var (
	ErrSearch = syscall.ESRCH // no such process (dead, recycled, or not visible)
	ErrPerm   = syscall.EPERM // insufficient credentials or forged siginfo
	ErrInval  = syscall.EINVAL
	ErrBadF   = syscall.EBADF
)

// si_code values relevant to the authorization rules. Negative si_code
// values come from user space; non-negative ones are kernel-generated and
// must never be forgeable toward another process.
const (
	SIUser    = 0   // sent by kill/sigqueue
	SIQueue   = -1  // sent by sigqueue with user payload
	SITkill   = -6  // sent by tkill/tgkill
	SIAsyncNL = -60 // sent by sigqueue without payload
)

// maxSignal is the highest signal number accepted by the interceptor.
const maxSignal = 64

// Caller describes the sandboxed process requesting the operation.
type Caller struct {
	PID        int    // caller's own pid in its PID namespace
	UID        uint32 // caller's real uid
	HasCapKill bool   // caller holds CAP_KILL in the relevant user namespace
	NsID       uint64 // identifier of the caller's PID namespace
}

// ProcInfo is a snapshot of a process as observed from a PID namespace.
type ProcInfo struct {
	PID       int    // pid in the observing namespace
	StartTime uint64 // boot-time-based start timestamp; disambiguates pid reuse
	UID       uint32 // real uid of the process owner
}

// ProcTable abstracts process lookup so the authorization logic stays
// testable. On Linux it is backed by /proc (see proc_linux.go).
type ProcTable interface {
	// Lookup resolves pid as seen from the PID namespace nsID. The second
	// return value is false when no such process exists or the pid is not
	// visible from that namespace.
	Lookup(pid int, nsID uint64) (ProcInfo, bool)
}

// Siginfo is the caller-supplied siginfo_t for pidfd_send_signal. Only the
// fields relevant to authorization are modeled.
type Siginfo struct {
	Signo int // must equal the signal argument
	Code  int // si_code
}

// boundPidfd is the pinned identity of a process at pidfd creation time.
type boundPidfd struct {
	pid   int
	start uint64
	nsID  uint64 // PID namespace the pidfd was created in
}

// Registry tracks pidfds handed to sandboxed callers and authorizes
// pidfd_send_signal requests against them.
type Registry struct {
	table  ProcTable
	mu     sync.Mutex
	nextFD int
	fds    map[int]boundPidfd
}

// NewRegistry returns a Registry resolving processes through table.
func NewRegistry(table ProcTable) *Registry {
	return &Registry{table: table, nextFD: 3, fds: make(map[int]boundPidfd)}
}

// Open creates a pidfd for pid on behalf of caller, pinning the target's
// current start-time. It fails with ESRCH when the target does not exist
// or is not visible from the caller's PID namespace.
func (r *Registry) Open(caller Caller, pid int) (int, error) {
	if pid <= 0 {
		return -1, ErrInval
	}
	info, ok := r.table.Lookup(pid, caller.NsID)
	if !ok {
		return -1, ErrSearch
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	fd := r.nextFD
	r.nextFD++
	r.fds[fd] = boundPidfd{pid: info.PID, start: info.StartTime, nsID: caller.NsID}
	return fd, nil
}

// Close releases a pidfd.
func (r *Registry) Close(fd int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.fds[fd]; !ok {
		return ErrBadF
	}
	delete(r.fds, fd)
	return nil
}

// SendSignal authorizes a pidfd_send_signal(fd, sig, info, flags) request.
// A nil error means the interceptor may perform the real syscall.
func (r *Registry) SendSignal(caller Caller, fd, sig int, info *Siginfo, flags int) error {
	if flags != 0 {
		return ErrInval
	}
	if sig < 0 || sig > maxSignal {
		return ErrInval
	}

	r.mu.Lock()
	bound, ok := r.fds[fd]
	r.mu.Unlock()
	if !ok {
		return ErrBadF
	}

	// Re-resolve the pinned pid in the caller's PID namespace. A process
	// invisible from the caller's namespace is indistinguishable from a
	// dead one.
	target, ok := r.table.Lookup(bound.pid, caller.NsID)
	if !ok {
		return ErrSearch
	}
	// The pid survived but may name a different, recycled process. The
	// pinned start-time must match exactly, otherwise the original target
	// has exited and the pid now belongs to someone else.
	if target.StartTime != bound.start {
		return ErrSearch
	}

	// Credential check: same real uid as the target, or CAP_KILL.
	if caller.UID != target.UID && !caller.HasCapKill {
		return ErrPerm
	}

	if info != nil {
		if info.Signo != sig {
			return ErrInval
		}
		// Kernel-only (non-negative) or tkill si_code values may only be
		// sent to the caller itself; toward any other process they are
		// treated as forged.
		if target.PID != caller.PID && (info.Code >= 0 || info.Code == SITkill) {
			return ErrPerm
		}
	}
	return nil
}
