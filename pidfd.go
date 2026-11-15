package netsec

import (
	"sync"
	"sync/atomic"
	"syscall"
)

var nextPIDFDID atomic.Uint64

// PIDFD is an authorization binding for a kernel pidfd.  The zero value is
// invalid; create bindings with a Registry.  The bound PID is never trusted
// by itself: every send rechecks the process start time so a recycled PID is
// rejected with ESRCH.
type PIDFD struct {
	mu       sync.RWMutex
	registry *Registry
	id       uint64
	fd       int
	fdKey    fdKey
	pid      int
	start    uint64
	uid      uint32
	euid     uint32
	suid     uint32
	pidNS    uint64
	userNS   uint64
	identity PIDFDIdentity
	hasIdent bool
	owner    Caller
	valid    bool
	closed   bool
}

// PID returns the target PID recorded when the pidfd was created.
func (p *PIDFD) PID() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pid
}

// FD returns the pidfd number, or -1 when the binding was created without an
// associated file descriptor.
func (p *PIDFD) FD() int {
	if p == nil {
		return -1
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.fd
}

// StartTime returns the bound process start timestamp (clock ticks since
// boot).
func (p *PIDFD) StartTime() uint64 {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.start
}

// Closed reports whether the binding has been released.
func (p *PIDFD) Closed() bool {
	if p == nil {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.closed
}

// Close releases the binding.  Further sends fail with EBADF.
func (p *PIDFD) Close() error {
	if p == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "nil pidfd")
	}
	p.mu.RLock()
	r := p.registry
	p.mu.RUnlock()
	if r == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "pidfd has no registry")
	}
	return r.Close(p)
}

// Authorize applies the pidfd_send_signal policy without delivering a
// signal.  It accepts a signal, a Caller, a Siginfo and flags in any order,
// so it can be called directly from a syscall decoder.  If no Caller is
// supplied, the pidfd creator's identity is used.
func (p *PIDFD) Authorize(args ...any) error {
	caller, sig, info, flags, err := parseSignalArgs(args)
	if err != nil {
		return err
	}
	if !callerSet(caller) && p != nil {
		p.mu.RLock()
		caller = p.owner
		p.mu.RUnlock()
	}
	return p.AuthorizeAs(caller, sig, info, flags)
}

// AuthorizeAs is the explicitly typed authorization entry point.
func (p *PIDFD) AuthorizeAs(caller Caller, sig any, info *Siginfo, flags any) error {
	if p == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "nil pidfd")
	}
	p.mu.RLock()
	r := p.registry
	p.mu.RUnlock()
	if r == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "pidfd has no registry")
	}
	signo, ok := signalNumber(sig)
	if !ok {
		return authErr(ErrInvalidSignal, syscall.EINVAL, "unsupported signal value %T", sig)
	}
	fl, ok := flagsValue(flags)
	if !ok {
		return authErr(ErrInvalidFlags, syscall.EINVAL, "unsupported flags value %T", flags)
	}
	return r.Authorize(p, caller, signo, info, fl)
}

// SendSignal authorizes and delivers a signal through the binding.  It
// accepts a signal, a Caller, a Siginfo and flags in any order.  If no
// Caller is supplied, the pidfd creator's identity is used; trusted callers
// should pass the intercepted syscall's actual sender explicitly.
func (p *PIDFD) SendSignal(args ...any) error {
	caller, sig, info, flags, err := parseSignalArgs(args)
	if err != nil {
		return err
	}
	if !callerSet(caller) && p != nil {
		p.mu.RLock()
		caller = p.owner
		p.mu.RUnlock()
	}
	return p.SendAuthorized(caller, sig, info, flags)
}

// SendAuthorized is the explicitly typed send entry point.  The caller
// identity is the security principal issuing the intercepted syscall, not
// necessarily the process that created the pidfd.
func (p *PIDFD) SendAuthorized(caller Caller, sig any, info *Siginfo, flags any) error {
	if p == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "nil pidfd")
	}
	p.mu.RLock()
	r := p.registry
	p.mu.RUnlock()
	if r == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "pidfd has no registry")
	}
	signo, ok := signalNumber(sig)
	if !ok {
		return authErr(ErrInvalidSignal, syscall.EINVAL, "unsupported signal value %T", sig)
	}
	fl, ok := flagsValue(flags)
	if !ok {
		return authErr(ErrInvalidFlags, syscall.EINVAL, "unsupported flags value %T", flags)
	}
	return r.Send(p, caller, signo, info, fl)
}

// SendSignalAs is the same operation with the signal arguments first, for
// interceptors that already have an explicit caller context.
func (p *PIDFD) SendSignalAs(sig any, info *Siginfo, flags any, caller Caller) error {
	return p.SendAuthorized(caller, sig, info, flags)
}

// Signal is a permissive compatibility entry point.  It accepts a signal,
// optional siginfo, optional flags, and an optional Caller in any order.
// When no caller is supplied the pidfd's creator is used.  New code should
// prefer SendSignal.
func (p *PIDFD) Signal(args ...any) error {
	caller, sig, info, flags, err := parseSignalArgs(args)
	if err != nil {
		return err
	}
	if !callerSet(caller) && p != nil {
		p.mu.RLock()
		caller = p.owner
		p.mu.RUnlock()
	}
	return p.SendAuthorized(caller, sig, info, flags)
}

func callerSet(c Caller) bool {
	return c.PID != 0 || c.UID != 0 || c.EUID != 0 || c.PIDNamespace != 0 ||
		c.UserNamespace != 0 || c.CapKill || len(c.VisiblePIDNamespaces) != 0
}

func parseSignalArgs(args []any) (Caller, int, *Siginfo, uint32, error) {
	var (
		caller  Caller
		sig     int
		info    *Siginfo
		flags   uint32
		haveSig bool
		haveFl  bool
	)
	for _, arg := range args {
		switch v := arg.(type) {
		case Caller:
			caller = v
		case *Caller:
			if v != nil {
				caller = *v
			}
		case Siginfo:
			s := v
			info = &s
		case *Siginfo:
			info = v
		case nil:
		default:
			if n, ok := signalNumber(v); ok {
				if !haveSig {
					sig, haveSig = n, true
				} else if !haveFl {
					fl, ok := flagsValue(v)
					if !ok {
						return Caller{}, 0, nil, 0, authErr(ErrInvalidFlags, syscall.EINVAL, "invalid flags")
					}
					flags, haveFl = fl, true
				}
				continue
			}
			return Caller{}, 0, nil, 0, authErr(ErrInvalidSignal, syscall.EINVAL, "unsupported argument %T", arg)
		}
	}
	if !haveSig {
		return Caller{}, 0, nil, 0, authErr(ErrInvalidSignal, syscall.EINVAL, "missing signal")
	}
	return caller, sig, info, flags, nil
}
