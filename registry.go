package netsec

import (
	"errors"
	"sync"
	"syscall"
)

// ErrProcessNotFound is returned by a Host when a PID is not present (or
// not visible) in the caller's PID namespace.
var ErrProcessNotFound = errors.New("process not found")

// Host supplies the platform-specific process state and signal delivery for
// a Registry.  LookupProcess must resolve pid from the intercepted caller's
// PID namespace, or return ErrProcessNotFound when it is not visible.
//
// SendSignal is called only after every authorization check succeeds.
type Host interface {
	LookupProcess(pid int) (ProcessInfo, error)
	SendSignal(fd, pid int, sig int, info *Siginfo, flags uint32) error
}

// VisibilityChecker is optionally implemented by a Host that can determine
// whether target is visible from caller's PID namespace, including ancestor
// and descendant relationships that cannot be inferred from namespace IDs
// alone.
type VisibilityChecker interface {
	CanSee(caller Caller, target ProcessInfo) bool
}

// PIDFDIdentity identifies the kernel object behind a file descriptor.  A
// Registry records it at bind time and verifies it again before delivery,
// preventing a recycled fd number from being used to signal another
// process.
type PIDFDIdentity struct {
	Device uint64
	Inode  uint64
}

// PIDFDVerifier is optionally implemented by a Host that can inspect a
// pidfd.  Returning an error means the fd is no longer usable.
type PIDFDVerifier interface {
	PIDFDIdentity(fd int) (PIDFDIdentity, error)
}

// PIDFDTargetResolver is optionally implemented by a Host that can report
// which PID a pidfd actually refers to.  A zero PID means the information is
// unavailable on this kernel.
type PIDFDTargetResolver interface {
	PIDFDTarget(fd int) (int, error)
}

// PIDFDOpener is optionally implemented by a Host that can create a pidfd.
// The default NewPIDFD path uses it when no descriptor was supplied.
type PIDFDOpener interface {
	OpenPIDFD(pid int) (int, error)
}

type fdKey struct {
	pid int
	fd  int
}

// Registry owns pidfd bindings and performs the authorization checks
// required by pidfd_send_signal.
type Registry struct {
	mu      sync.Mutex
	host    Host
	handles map[*PIDFD]struct{}
	fds     map[fdKey]*PIDFD
}

// NewRegistry returns a Registry backed by host.
func NewRegistry(host Host) *Registry {
	return &Registry{
		host:    host,
		handles: make(map[*PIDFD]struct{}),
		fds:     make(map[fdKey]*PIDFD),
	}
}

// Host returns the registry's host.
func (r *Registry) Host() Host {
	if r == nil {
		return nil
	}
	return r.host
}

// Bind records a pidfd returned for pid.  fd is the descriptor number
// observed by the intercepted pidfd_open call; pass -1 when the authorizer
// is being used without a kernel fd.  The target PID and start time are
// captured immediately.
func (r *Registry) Bind(caller Caller, fd, pid int) (*PIDFD, error) {
	if r == nil || r.host == nil {
		return nil, authErr(ErrUnsupported, syscall.ENOSYS, "registry has no host")
	}

	info, err := r.host.LookupProcess(pid)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return nil, err
		}
		return nil, targetGone(pid, 0)
	}

	p := &PIDFD{
		registry: r,
		id:       nextPIDFDID.Add(1),
		fd:       fd,
		pid:      pid,
		start:    info.StartTime,
		uid:      info.UID,
		euid:     info.EUID,
		suid:     info.SUID,
		pidNS:    info.PIDNamespace,
		userNS:   info.UserNamespace,
		owner:    caller,
		valid:    true,
	}

	if fd >= 0 {
		if tr, ok := r.host.(PIDFDTargetResolver); ok {
			targetPID, err := tr.PIDFDTarget(fd)
			if err != nil {
				return nil, authErr(ErrPIDFDNotBound, syscall.EBADF,
					"cannot resolve pidfd %d target: %v", fd, err)
			}
			if targetPID != 0 && targetPID != pid {
				return nil, authErr(ErrPIDFDChanged, syscall.EBADF,
					"pidfd %d refers to pid %d, not %d", fd, targetPID, pid)
			}
		}
		if v, ok := r.host.(PIDFDVerifier); ok {
			id, err := v.PIDFDIdentity(fd)
			if err != nil {
				return nil, authErr(ErrPIDFDNotBound, syscall.EBADF,
					"cannot inspect pidfd %d: %v", fd, err)
			}
			p.identity = id
			p.hasIdent = true
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if fd >= 0 {
		key := fdKey{pid: caller.PID, fd: fd}
		if old := r.fds[key]; old != nil {
			old.mu.Lock()
			old.closed = true
			old.mu.Unlock()
			delete(r.handles, old)
		}
		p.fdKey = key
		r.fds[key] = p
	}
	r.handles[p] = struct{}{}
	return p, nil
}

// Open binds a pidfd target without associating a kernel descriptor.
func (r *Registry) Open(caller Caller, pid int) (*PIDFD, error) {
	return r.Bind(caller, -1, pid)
}

// OpenFD is an alias for Bind.
func (r *Registry) OpenFD(caller Caller, fd, pid int) (*PIDFD, error) {
	return r.Bind(caller, fd, pid)
}

// RegisterPIDFD is an alias for Bind.
func (r *Registry) RegisterPIDFD(caller Caller, fd, pid int) (*PIDFD, error) {
	return r.Bind(caller, fd, pid)
}

// LookupFD returns the binding registered for caller's fd number.
func (r *Registry) LookupFD(caller Caller, fd int) (*PIDFD, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	p := r.fds[fdKey{pid: caller.PID, fd: fd}]
	r.mu.Unlock()
	return p, p != nil
}

// Authorize resolves the bound process and applies policy without sending a
// signal.
func (r *Registry) Authorize(p *PIDFD, caller Caller, sig any, info *Siginfo, flags any) error {
	signo, fl, err := normalizeSignalArgs(sig, flags)
	if err != nil {
		return err
	}
	_, err = r.authorize(p, caller, signo, info, fl)
	return err
}

func normalizeSignalArgs(sig, flags any) (int, uint32, error) {
	signo, ok := signalNumber(sig)
	if !ok {
		return 0, 0, authErr(ErrInvalidSignal, syscall.EINVAL, "unsupported signal value %T", sig)
	}
	fl, ok := flagsValue(flags)
	if !ok {
		return 0, 0, authErr(ErrInvalidFlags, syscall.EINVAL, "unsupported flags value %T", flags)
	}
	return signo, fl, nil
}

func (r *Registry) authorize(p *PIDFD, caller Caller, sig int, info *Siginfo, flags uint32) (*ProcessInfo, error) {
	if r == nil || r.host == nil {
		return nil, authErr(ErrUnsupported, syscall.ENOSYS, "registry has no host")
	}
	if p == nil {
		return nil, authErr(ErrPIDFDNotBound, syscall.EBADF, "nil pidfd")
	}

	p.mu.RLock()
	fd := p.fd
	pid := p.pid
	start := p.start
	closed := p.closed
	owner := p.owner
	wantIdentity := p.identity
	hasIdentity := p.hasIdent
	p.mu.RUnlock()

	if closed {
		return nil, authErr(ErrPIDFDClosed, syscall.EBADF, "pidfd %d", fd)
	}

	r.mu.Lock()
	_, registered := r.handles[p]
	r.mu.Unlock()
	if !registered {
		return nil, authErr(ErrPIDFDNotBound, syscall.EBADF, "pidfd was not created by this registry")
	}

	if flags != 0 {
		return nil, authErr(ErrInvalidFlags, syscall.EINVAL, "flags=%#x", flags)
	}
	if !callerSet(caller) {
		caller = owner
	}

	if fd >= 0 && hasIdentity {
		if v, ok := r.host.(PIDFDVerifier); ok {
			got, err := v.PIDFDIdentity(fd)
			if err != nil || got != wantIdentity {
				return nil, authErr(ErrPIDFDChanged, syscall.EBADF,
					"pidfd %d no longer refers to the bound object", fd)
			}
		}
	}
	if fd >= 0 {
		if tr, ok := r.host.(PIDFDTargetResolver); ok {
			targetPID, err := tr.PIDFDTarget(fd)
			if err != nil {
				return nil, authErr(ErrPIDFDChanged, syscall.EBADF,
					"cannot resolve pidfd %d target: %v", fd, err)
			}
			if targetPID != 0 && targetPID != pid {
				return nil, authErr(ErrPIDFDChanged, syscall.EBADF,
					"pidfd %d now refers to pid %d, not %d", fd, targetPID, pid)
			}
		}
	}

	target, err := r.host.LookupProcess(pid)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return nil, err
		}
		return nil, targetGone(pid, start)
	}
	if target.PID == 0 {
		target.PID = pid
	}

	req := SendRequest{
		PIDFD:   p,
		Caller:  caller,
		Signal:  sig,
		Siginfo: info,
		Flags:   flags,
		Target:  &target,
	}
	if v, ok := r.host.(VisibilityChecker); ok {
		req.VisibilityChecked = true
		req.Visible = v.CanSee(caller, target)
	}
	if err := AuthorizeSend(req); err != nil {
		return nil, err
	}
	return &target, nil
}

// Send authorizes and delivers the signal.  It is an error for any policy
// check to fail; in that case the host's SendSignal is never called.
func (r *Registry) Send(p *PIDFD, caller Caller, sig any, info *Siginfo, flags any) error {
	signo, fl, err := normalizeSignalArgs(sig, flags)
	if err != nil {
		return err
	}
	target, err := r.authorize(p, caller, signo, info, fl)
	if err != nil {
		return err
	}

	// The binding may have been closed after authorization.  Recheck the
	// delivery state so a concurrent close cannot turn into a send.
	p.mu.RLock()
	fd := p.fd
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return authErr(ErrPIDFDClosed, syscall.EBADF, "pidfd %d", fd)
	}
	return r.host.SendSignal(fd, target.PID, signo, info, fl)
}

// SendFD is the raw-descriptor entry point used by a syscall interceptor.
// The fd must have been registered by Bind for the same caller PID.
func (r *Registry) SendFD(caller Caller, fd int, sig any, info *Siginfo, flags any) error {
	p, ok := r.LookupFD(caller, fd)
	if !ok {
		return authErr(ErrPIDFDNotBound, syscall.EBADF,
			"fd %d is not a registered pidfd", fd)
	}
	return r.Send(p, caller, sig, info, flags)
}

// AuthorizeFD is the non-delivering counterpart of SendFD.
func (r *Registry) AuthorizeFD(caller Caller, fd int, sig any, info *Siginfo, flags any) error {
	p, ok := r.LookupFD(caller, fd)
	if !ok {
		return authErr(ErrPIDFDNotBound, syscall.EBADF,
			"fd %d is not a registered pidfd", fd)
	}
	return r.Authorize(p, caller, sig, info, flags)
}

// Close releases a binding.  Closing also invalidates the fd mapping so a
// later dup2 or open that reuses the descriptor number is not accepted.
func (r *Registry) Close(p *PIDFD) error {
	if r == nil || p == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "nil pidfd")
	}
	r.mu.Lock()
	if _, ok := r.handles[p]; !ok {
		r.mu.Unlock()
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "pidfd was not created by this registry")
	}
	delete(r.handles, p)
	p.mu.Lock()
	p.closed = true
	key := p.fdKey
	hadKey := p.fd >= 0
	p.mu.Unlock()
	if hadKey {
		if r.fds[key] == p {
			delete(r.fds, key)
		}
	}
	r.mu.Unlock()
	return nil
}

// CloseFD releases the binding for caller's fd.
func (r *Registry) CloseFD(caller Caller, fd int) error {
	p, ok := r.LookupFD(caller, fd)
	if !ok {
		return nil
	}
	return r.Close(p)
}
