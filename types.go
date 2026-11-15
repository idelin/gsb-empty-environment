package netsec

import "reflect"

// Signal numbers used by siginfo.si_code.  These values are part of the
// Linux userspace ABI.  SI_TKILL is deliberately called out by the pidfd
// authorization rules: it must never be accepted when a caller supplies
// siginfo for another process.
const (
	SI_USER    = 0
	SI_QUEUE   = -1
	SI_TIMER   = -2
	SI_MESGQ   = -3
	SI_ASYNCIO = -4
	SI_SIGIO   = -5
	SI_TKILL   = -6
)

// Siginfo is the subset of siginfo_t that the syscall interceptor needs to
// authenticate.  The layout is intentionally independent of the native
// siginfo_t so callers can validate a decoded record on any host.
type Siginfo struct {
	Signo int
	Errno int
	Code  int
	PID   int
	UID   uint32
	Value int64
}

// NewSiginfo returns a siginfo record with the fields relevant to pidfd
// authorization populated.
func NewSiginfo(signo, code int) *Siginfo {
	return &Siginfo{Signo: signo, Code: code}
}

// Caller describes the process that issued pidfd_send_signal.  UID is the
// sender's effective user ID.  PIDNamespace identifies the caller's PID
// namespace, while VisiblePIDNamespaces may contain descendant namespaces
// that are visible from it (for example, child PID namespaces).
//
// CapKill must only be set by the interceptor after it has established, in
// the target's user namespace, that the sender holds CAP_KILL.
type Caller struct {
	PID                  int
	UID                  uint32
	EUID                 uint32
	PIDNamespace         uint64
	UserNamespace        uint64
	CapKill              bool
	VisiblePIDNamespaces []uint64
}

// ProcessInfo is a snapshot of a process.  StartTime is the process start
// time in clock ticks since boot (field 22 of /proc/<pid>/stat).  A pidfd
// binding stores it so that a recycled PID cannot be mistaken for the
// process that was originally bound.
type ProcessInfo struct {
	PID           int
	StartTime     uint64
	UID           uint32
	EUID          uint32
	SUID          uint32
	PIDNamespace  uint64
	UserNamespace uint64
	// Exited is set when the host can see the process is a zombie or has
	// otherwise exited but the PID has not yet been recycled.
	Exited bool
}

// Aliases kept for callers that use more descriptive type names.
type (
	Process    = ProcessInfo
	TargetInfo = ProcessInfo
	SigInfo    = Siginfo
)

// signalNumber converts the integer-like signal values commonly produced by
// syscall interceptors (int, syscall.Signal, uint32, ...) to an int.
func signalNumber(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(u), true
	default:
		return 0, false
	}
}

// flagsValue accepts the same integer-like forms as signalNumber.
func flagsValue(v any) (uint32, bool) {
	if v == nil {
		return 0, true
	}
	rv := reflect.ValueOf(v)
	var u uint64
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := rv.Int()
		if n < 0 {
			return 0, false
		}
		u = uint64(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u = rv.Uint()
	default:
		return 0, false
	}
	if u > uint64(^uint32(0)) {
		return 0, false
	}
	return uint32(u), true
}
