package hostlock

import "syscall"

// Prober reports liveness of a process. It is an injection point: tests and
// simulations can substitute a fake process table.
type Prober interface {
	// Probe reports whether pid currently refers to a live process, and an
	// opaque signature identifying that process instance (typically the
	// process start time). The signature is "" on platforms that cannot
	// provide one; callers must then treat liveness as advisory only.
	Probe(pid int) (alive bool, startSig string)
}

// SystemProber probes real OS processes.
type SystemProber struct{}

// Probe implements Prober using kill(pid, 0) plus a platform-specific
// process-start signature to detect PID reuse.
func (SystemProber) Probe(pid int) (alive bool, startSig string) {
	if pid <= 0 {
		return false, ""
	}
	err := syscall.Kill(pid, 0)
	if err == syscall.ESRCH {
		return false, ""
	}
	if err != nil && err != syscall.EPERM {
		// EPERM means the process exists but belongs to another user.
		return false, ""
	}
	return true, processStartSig(pid)
}
