package netsec

import (
	"os"
	"sync"
	"syscall"
)

// Type aliases for callers that use alternate spelling or terminology.
type (
	Pidfd           = PIDFD
	PidFD           = PIDFD
	Authorizer      = Registry
	ProcessSnapshot = ProcessInfo
	Sender          = Caller
	SignalSiginfo   = Siginfo
	SignalRequest   = SendRequest
)

var (
	defaultMu       sync.RWMutex
	defaultRegistry = NewRegistry(NewOSHost())
)

// DefaultRegistry returns the process-wide registry.  A syscall interceptor
// normally installs its own Host with SetDefaultHost at startup.
func DefaultRegistry() *Registry {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRegistry
}

// SetDefaultHost replaces the process-wide registry and its host.  Existing
// PIDFD bindings belong to the old registry and are no longer valid.
func SetDefaultHost(h Host) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultRegistry = NewRegistry(h)
}

func registryFromOptions(opts []any) (*Registry, int, Caller) {
	r := DefaultRegistry()
	fd := -1
	caller := Caller{PID: os.Getpid(), UID: uint32(os.Getuid()), EUID: uint32(os.Geteuid())}
	for _, opt := range opts {
		switch v := opt.(type) {
		case *Registry:
			if v != nil {
				r = v
			}
		case Caller:
			caller = v
		case *Caller:
			if v != nil {
				caller = *v
			}
		default:
			if n, ok := signalNumber(opt); ok && n >= 0 {
				fd = n
			}
		}
	}
	return r, fd, caller
}

// NewPIDFD binds pid in the default registry.  Optional arguments may be a
// *Registry, a Caller, and an fd number.  New code should use
// Registry.Bind, which makes the trust boundary explicit.
func NewPIDFD(pid int, opts ...any) (*PIDFD, error) {
	r, fd, caller := registryFromOptions(opts)
	if fd < 0 {
		if opener, ok := r.Host().(PIDFDOpener); ok {
			opened, err := opener.OpenPIDFD(pid)
			if err != nil {
				return nil, err
			}
			fd = opened
		}
	}
	return r.Bind(caller, fd, pid)
}

// NewAuthorizer is an alias for NewRegistry.
func NewAuthorizer(h Host) *Registry {
	return NewRegistry(h)
}

// OpenPIDFD is an alias for NewPIDFD.
func OpenPIDFD(pid int, opts ...any) (*PIDFD, error) {
	return NewPIDFD(pid, opts...)
}

// CreatePIDFD is an alias for NewPIDFD.
func CreatePIDFD(pid int, opts ...any) (*PIDFD, error) {
	return NewPIDFD(pid, opts...)
}

// BindPIDFD binds an fd/pid pair in the default registry or in a registry
// supplied as an option.
func BindPIDFD(fd, pid int, opts ...Caller) (*PIDFD, error) {
	caller := Caller{PID: os.Getpid(), UID: uint32(os.Getuid()), EUID: uint32(os.Geteuid())}
	if len(opts) != 0 {
		caller = opts[0]
	}
	return DefaultRegistry().Bind(caller, fd, pid)
}

// RegisterPIDFD is an alias for BindPIDFD.
func RegisterPIDFD(fd, pid int, caller ...Caller) (*PIDFD, error) {
	return BindPIDFD(fd, pid, caller...)
}

// SendSignal authorizes and delivers a signal through handle.  The
// remaining arguments may contain a signal, a Caller, a Siginfo and flags
// in any order.
func SendSignal(handle *PIDFD, args ...any) error {
	return handle.SendSignal(args...)
}

// AuthorizeSendSignal performs the same checks as SendSignal without
// delivering anything.
func AuthorizeSendSignal(handle *PIDFD, args ...any) error {
	return handle.Authorize(args...)
}

// Signal is a permissive compatibility entry point.  The first argument
// must be a PIDFD; the remaining arguments may contain a signal, a Caller,
// a Siginfo and flags in any order.
func Signal(args ...any) error {
	if len(args) == 0 {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "missing pidfd")
	}
	p, ok := args[0].(*PIDFD)
	if !ok {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "first argument is %T, want *PIDFD", args[0])
	}
	return p.Signal(args[1:]...)
}

// SendPIDFDSignal is an alias for Signal.
func SendPIDFDSignal(args ...any) error { return Signal(args...) }

// Session binds a Registry to the identity of the process issuing syscalls.
// It is the most convenient interface for an interceptor.
type Session struct {
	Registry *Registry
	Caller   Caller
}

// ForCaller returns a Session for caller.
func (r *Registry) ForCaller(caller Caller) *Session {
	return &Session{Registry: r, Caller: caller}
}

// Bind binds pid for the session's caller.
func (s *Session) Bind(pid int) (*PIDFD, error) {
	return s.Registry.Bind(s.Caller, -1, pid)
}

// BindFD binds an fd/pid pair for the session's caller.
func (s *Session) BindFD(fd, pid int) (*PIDFD, error) {
	return s.Registry.Bind(s.Caller, fd, pid)
}

// Send authorizes and delivers a signal.
func (s *Session) Send(p *PIDFD, sig any, info *Siginfo, flags any) error {
	return p.SendAuthorized(s.Caller, sig, info, flags)
}

// SendFD authorizes and delivers a signal for a registered fd.
func (s *Session) SendFD(fd, sig int, info *Siginfo, flags uint32) error {
	return s.Registry.SendFD(s.Caller, fd, sig, info, flags)
}
