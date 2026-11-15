package netsec

import (
	"syscall"
)

// SendRequest is the complete input to a pidfd signal authorization check.
// Target is the process snapshot observed immediately before delivery.  A
// nil Target means the bound process is gone.
//
// VisibilityChecked and Visible allow a host that understands PID namespace
// ancestry to override the package's conservative namespace comparison.
type SendRequest struct {
	PIDFD             *PIDFD
	Caller            Caller
	Signal            int
	Siginfo           *Siginfo
	Flags             uint32
	Target            *ProcessInfo
	VisibilityChecked bool
	Visible           bool
}

// AuthorizeSend performs all policy checks without delivering a signal.
func AuthorizeSend(req SendRequest) error {
	p := req.PIDFD
	if p == nil {
		return authErr(ErrPIDFDNotBound, syscall.EBADF, "nil pidfd")
	}

	p.mu.RLock()
	valid := p.valid
	closed := p.closed
	fd := p.fd
	boundPID := p.pid
	boundStart := p.start
	boundPIDNS := p.pidNS
	p.mu.RUnlock()

	if !valid || closed {
		kind := ErrPIDFDNotBound
		if closed {
			kind = ErrPIDFDClosed
		}
		return authErr(kind, syscall.EBADF, "pidfd %d", fd)
	}

	if req.Flags != 0 {
		return authErr(ErrInvalidFlags, syscall.EINVAL, "flags=%#x", req.Flags)
	}
	if req.Signal < 0 || req.Signal > 64 {
		return authErr(ErrInvalidSignal, syscall.EINVAL, "signal=%d", req.Signal)
	}

	if req.Target == nil || req.Target.Exited {
		return targetGone(boundPID, boundStart)
	}
	t := *req.Target

	if t.PID != 0 && t.PID != boundPID {
		return pidReused(boundPID, boundStart, t.StartTime)
	}
	if t.StartTime != boundStart {
		return pidReused(boundPID, boundStart, t.StartTime)
	}
	if boundPIDNS != 0 && t.PIDNamespace != 0 && t.PIDNamespace != boundPIDNS {
		return authErr(ErrTargetNotSeen, syscall.ESRCH,
			"pid %d moved out of bound PID namespace %d", boundPID, boundPIDNS)
	}

	visible := visibleFrom(req.Caller, t)
	if req.VisibilityChecked {
		visible = req.Visible
	}
	if !visible {
		return authErr(ErrTargetNotSeen, syscall.ESRCH,
			"pid %d is not visible from PID namespace %d", boundPID, req.Caller.PIDNamespace)
	}

	if !callerMaySignal(req.Caller, t) {
		return authErr(ErrPermission, syscall.EPERM,
			"sender uid %d may not signal target uid %d", req.Caller.UID, t.UID)
	}

	self := req.Caller.PID != 0 && t.PID != 0 && req.Caller.PID == t.PID
	if err := checkSiginfo(req.Signal, req.Siginfo, self); err != nil {
		return err
	}

	return nil
}

// CheckSendSignal is an alias for AuthorizeSend.
func CheckSendSignal(req SendRequest) error { return AuthorizeSend(req) }

func visibleFrom(caller Caller, target ProcessInfo) bool {
	// Unknown namespace metadata is left to the host's lookup, which must
	// have resolved the PID from the caller's namespace in the first place.
	if caller.PIDNamespace == 0 || target.PIDNamespace == 0 {
		return true
	}
	if caller.PIDNamespace == target.PIDNamespace {
		return true
	}
	if caller.PID != 0 && caller.PID == target.PID {
		return true
	}
	for _, ns := range caller.VisiblePIDNamespaces {
		if ns == target.PIDNamespace {
			return true
		}
	}
	return false
}

func callerMaySignal(caller Caller, target ProcessInfo) bool {
	if caller.CapKill {
		return true
	}
	if caller.UID == target.UID {
		return true
	}
	if target.EUID != 0 && caller.UID == target.EUID {
		return true
	}
	if target.SUID != 0 && caller.UID == target.SUID {
		return true
	}
	if caller.EUID != 0 {
		if caller.EUID == target.UID {
			return true
		}
		if target.EUID != 0 && caller.EUID == target.EUID {
			return true
		}
		if target.SUID != 0 && caller.EUID == target.SUID {
			return true
		}
	}
	return false
}

func checkSiginfo(sig int, info *Siginfo, self bool) error {
	if info == nil {
		return nil
	}
	if info.Signo != sig {
		return authErr(ErrInvalidSiginfo, syscall.EINVAL,
			"si_signo=%d, signal=%d", info.Signo, sig)
	}
	if !self && (info.Code >= 0 || info.Code == SI_TKILL) {
		return authErr(ErrForgedSiginfo, syscall.EPERM,
			"non-self sender supplied si_code=%d", info.Code)
	}
	return nil
}
