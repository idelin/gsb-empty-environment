// Package netsec implements the authorization layer a Go sandbox host calls
// from its pidfd_send_signal system-call interceptor.
//
// The package does not execute syscalls. It answers one question: given the
// caller's credentials, the pidfd it holds, and the arguments of
// pidfd_send_signal, should the host perform the signal delivery? The answer
// is nil (allow) or a syscall.Errno describing the exact Linux error the
// interceptor must return:
//
//   - syscall.EBADF: the pidfd number is unknown or was closed.
//   - syscall.ESRCH: the bound process has exited, its PID was reused by a
//     different process, or it is not visible from the caller's PID namespace.
//   - syscall.EPERM: the target is alive and visible but the caller may not
//     signal it.
//   - syscall.EINVAL: bad signal number, unsupported flags, or a siginfo that
//     mislabels the signal / spoofs si_code.
//
// Stolen or leaked pidfds are not a privilege on their own: every send is
// re-validated against the live process table and the current caller's
// credentials, so a pidfd inherited from another task cannot be used outside
// the ordinary kill(2) permission rules.
package netsec

import (
	"sync"
	"syscall"
)

// binding is the immutable identity captured when a pidfd is opened. It binds
// the descriptor to one kernel process incarnation, not merely to a PID.
type binding struct {
	pid       int
	startTime uint64
	ns        *PIDNamespace
}

// Authorizer owns the pidfd registry and performs the policy checks. It is
// safe for concurrent use; a sandbox host typically creates one per
// supervised process tree.
type Authorizer struct {
	mu       sync.RWMutex
	resolver ProcessResolver
	nextFD   int
	fds      map[int]binding
}

// NewAuthorizer creates an Authorizer backed by resolver. The resolver is the
// host's source of truth for currently running processes.
func NewAuthorizer(resolver ProcessResolver) *Authorizer {
	return &Authorizer{
		resolver: resolver,
		nextFD:   100,
		fds:      make(map[int]binding),
	}
}

// OpenPIDFD emulates opening a pidfd for the process occupying pid inside ns.
//
// The descriptor is bound to the target's PID together with its start
// timestamp, so a later PID reuse cannot be reached through it. It returns
// ESRCH when no live process holds the PID in that namespace. The returned
// integer is an opaque descriptor number the interceptor passes back to
// SendSignal and Close.
func (a *Authorizer) OpenPIDFD(ns *PIDNamespace, pid int) (int, error) {
	proc, ok := a.resolver.LookupByPID(ns, pid)
	if !ok || !proc.Alive {
		return 0, syscall.ESRCH
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	fd := a.nextFD
	a.nextFD++
	a.fds[fd] = binding{
		pid:       proc.PID,
		startTime: proc.StartTime,
		ns:        proc.NS,
	}
	return fd, nil
}

// Close releases a pidfd previously returned by OpenPIDFD. Sending through a
// closed descriptor afterwards fails with EBADF.
func (a *Authorizer) Close(fd int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fds, fd)
}

// SendSignal authorizes a pidfd_send_signal(pidfd, sig, info, flags) call.
//
// The checks run in the same order as the kernel path so the reported errno
// matches Linux behaviour:
//
//  1. flags must be zero (EINVAL).
//  2. sig must be a real signal in [1, MaxSignal] (EINVAL).
//  3. pidfd must identify an open descriptor (EBADF).
//  4. the bound process must still be the same live incarnation: same PID and
//     start timestamp in its PID namespace (ESRCH). This defeats PID reuse.
//  5. the target must be visible from the caller's PID namespace (ESRCH).
//  6. when info is non-nil it must be honest: si_signo equals sig, and for a
//     non-self target si_code must be a negative kernel code other than
//     SI_TKILL (EINVAL).
//  7. the caller must be the target itself, share the target's UID, or hold
//     CAP_KILL (EPERM).
func (a *Authorizer) SendSignal(caller Caller, fd, sig int, info *SigInfo, flags int) error {
	if flags != 0 {
		return syscall.EINVAL
	}
	if sig < 1 || sig > MaxSignal {
		return syscall.EINVAL
	}

	a.mu.RLock()
	bound, ok := a.fds[fd]
	a.mu.RUnlock()
	if !ok {
		return syscall.EBADF
	}

	target, ok := a.resolver.LookupByPID(bound.ns, bound.pid)
	if !ok || !target.Alive || target.StartTime != bound.startTime {
		// The original process exited. Even if the same PID was reused, the
		// start timestamp now belongs to a different incarnation.
		return syscall.ESRCH
	}

	if caller.NS == nil || !caller.NS.Sees(target.NS) {
		return syscall.ESRCH
	}

	if info != nil {
		if info.Signo != sig {
			return syscall.EINVAL
		}
		if target.ID != caller.SelfID && spoofsSICode(info.Code) {
			return syscall.EINVAL
		}
	}

	if target.ID != caller.SelfID &&
		caller.UID != target.UID &&
		!caller.CapKill {
		return syscall.EPERM
	}
	return nil
}

// spoofsSICode reports whether si_code claims an origin an unprivileged
// user-space sender is not allowed to present for a signal aimed at another
// process.
//
// Non-negative codes (SI_USER and friends) describe origins only the kernel is
// allowed to stamp on behalf of a process, and SI_TKILL impersonates a
// thread-directed tkill/tgkill delivery. Both are rejected for non-self
// targets; genuine kernel-generated negative codes such as SI_QUEUE pass.
func spoofsSICode(code int) bool {
	return code >= 0 || code == siTkill
}
