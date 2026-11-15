package netsec

import "syscall"

// Authorization failures are returned as the exact errno a syscall
// interceptor should give to the guest.  The named sentinels below are
// aliases for those errno values; errors.Is works with either form, and a
// direct comparison such as err == syscall.ESRCH also works.
//
// A policy denial and a delivery failure can therefore share an errno.  In
// particular, ErrTargetExited and ErrPIDReused are both ESRCH: the caller
// must not be able to distinguish an exited process from a recycled PID.
var (
	ErrPIDFDNotBound  error = syscall.EBADF
	ErrPIDFDClosed    error = syscall.EBADF
	ErrPIDFDChanged   error = syscall.EBADF
	ErrTargetExited   error = syscall.ESRCH
	ErrPIDReused      error = syscall.ESRCH
	ErrTargetNotSeen  error = syscall.ESRCH
	ErrPermission     error = syscall.EPERM
	ErrForgedSiginfo  error = syscall.EPERM
	ErrInvalidSiginfo error = syscall.EINVAL
	ErrInvalidFlags   error = syscall.EINVAL
	ErrInvalidSignal  error = syscall.EINVAL
	ErrUnsupported    error = syscall.ENOSYS
)

// Common alternative names for the sentinels above.
var (
	ErrNotPermitted = ErrPermission
	ErrNamespace    = ErrTargetNotSeen
	ErrSiginfo      = ErrForgedSiginfo
	ErrFlags        = ErrInvalidFlags
	ErrStalePIDFD   = ErrPIDFDChanged
)

// AuthError is the concrete error type returned by the authorization path.
// It is syscall.Errno, so errors.Is(err, syscall.ESRCH) and err ==
// syscall.ESRCH behave identically.
type AuthError = syscall.Errno

// authErr keeps call sites descriptive while returning the precise errno.
func authErr(_ error, errno error, _ string, _ ...any) error {
	return errno
}

func targetGone(_ int, _ uint64) error {
	return syscall.ESRCH
}

func pidReused(_ int, _, _ uint64) error {
	return syscall.ESRCH
}
