package netsec_test

import (
	"errors"
	"syscall"
	"testing"

	netsec "example.org/netsec/q0492"
)

const (
	targetPID   = 4242
	targetStart = uint64(100000)
	targetUID   = uint32(1000)
	callerPID   = 7000
	callerUID   = uint32(1000)
	nsA         = uint64(4026531836)
	nsB         = uint64(4026532500)
)

func fixture(t *testing.T, fd int) (*netsec.MemoryHost, *netsec.Registry, netsec.Caller, *netsec.PIDFD) {
	t.Helper()
	h := netsec.NewMemoryHost()
	h.AddProcess(netsec.ProcessInfo{
		PID:          targetPID,
		StartTime:    targetStart,
		UID:          targetUID,
		PIDNamespace: nsA,
	})
	id := netsec.PIDFDIdentity{Device: 1, Inode: 9}
	h.SetPIDFD(fd, id)
	r := netsec.NewRegistry(h)
	caller := netsec.Caller{PID: callerPID, UID: callerUID, PIDNamespace: nsA}
	p, err := r.Bind(caller, fd, targetPID)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return h, r, caller, p
}

func wantErr(t *testing.T, err, sentinel, errno error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if sentinel != nil && !errors.Is(err, sentinel) {
		t.Fatalf("error %v does not match %v", err, sentinel)
	}
	if errno != nil && !errors.Is(err, errno) {
		t.Fatalf("error %v does not match errno %v", err, errno)
	}
}

func TestSameUserIsAllowed(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	if err := r.Send(p, caller, int(syscall.SIGTERM), nil, 0); err != nil {
		t.Fatalf("Send: %v", err)
	}
	call, ok := h.LastSignal()
	if !ok {
		t.Fatal("host did not receive the signal")
	}
	if call.PID != targetPID || call.Signal != int(syscall.SIGTERM) || call.FD != 7 {
		t.Fatalf("unexpected delivery: %+v", call)
	}
}

func TestDifferentUserNeedsCapKill(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	other := caller
	other.UID = 1001
	err := r.Send(p, other, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrPermission, syscall.EPERM)
	if h.SignalCount() != 0 {
		t.Fatal("signal was delivered without permission")
	}

	other.CapKill = true
	if err := r.Send(p, other, int(syscall.SIGTERM), nil, 0); err != nil {
		t.Fatalf("CAP_KILL sender denied: %v", err)
	}
	if h.SignalCount() != 1 {
		t.Fatal("CAP_KILL sender should have been allowed")
	}
}

func TestPIDReuseNeverSignalsNewProcess(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	h.RemoveProcess(targetPID)
	h.AddProcess(netsec.ProcessInfo{
		PID:          targetPID,
		StartTime:    targetStart + 1,
		UID:          targetUID,
		PIDNamespace: nsA,
	})

	err := r.Send(p, caller, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrPIDReused, syscall.ESRCH)
	if h.SignalCount() != 0 {
		t.Fatal("signal was delivered to the reused PID")
	}
}

func TestExitedTargetReturnsESRCH(t *testing.T) {
	h, r, caller, p := fixture(t, 7)
	h.RemoveProcess(targetPID)

	err := r.Send(p, caller, int(syscall.SIGUSR1), nil, 0)
	wantErr(t, err, netsec.ErrTargetExited, syscall.ESRCH)
	if err != syscall.ESRCH {
		t.Fatalf("err = %v, want exact errno ESRCH", err)
	}
	if h.SignalCount() != 0 {
		t.Fatal("signal was delivered to an exited process")
	}
}

func TestZombieTargetReturnsESRCH(t *testing.T) {
	h, r, caller, p := fixture(t, 7)
	h.AddProcess(netsec.ProcessInfo{
		PID:       targetPID,
		StartTime: targetStart,
		UID:       targetUID,
		Exited:    true,
	})

	err := r.Send(p, caller, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrTargetExited, syscall.ESRCH)
	if h.SignalCount() != 0 {
		t.Fatal("signal was delivered to a zombie")
	}
}

func TestCrossNamespaceIsDeniedUnlessVisible(t *testing.T) {
	h := netsec.NewMemoryHost()
	h.AddProcess(netsec.ProcessInfo{
		PID:          targetPID,
		StartTime:    targetStart,
		UID:          targetUID,
		PIDNamespace: nsB,
	})
	h.SetPIDFD(7, netsec.PIDFDIdentity{Device: 1, Inode: 9})
	r := netsec.NewRegistry(h)
	caller := netsec.Caller{PID: callerPID, UID: callerUID, PIDNamespace: nsA}
	p, err := r.Bind(caller, 7, targetPID)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	err = r.Send(p, caller, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrTargetNotSeen, syscall.ESRCH)
	if h.SignalCount() != 0 {
		t.Fatal("signal crossed an invisible PID namespace boundary")
	}

	visible := caller
	visible.VisiblePIDNamespaces = []uint64{nsB}
	if err := r.Send(p, visible, int(syscall.SIGTERM), nil, 0); err != nil {
		t.Fatalf("visible child namespace denied: %v", err)
	}
	if h.SignalCount() != 1 {
		t.Fatal("visible target should have received the signal")
	}
}

func TestForgedSiginfoIsRejected(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	for _, code := range []int{0, 1, netsec.SI_USER, netsec.SI_TKILL} {
		info := netsec.NewSiginfo(int(syscall.SIGTERM), code)
		err := r.Send(p, caller, int(syscall.SIGTERM), info, 0)
		wantErr(t, err, netsec.ErrForgedSiginfo, syscall.EPERM)
	}
	if h.SignalCount() != 0 {
		t.Fatal("forged siginfo reached the host")
	}
}

func TestNegativeSiginfoCodeIsAccepted(t *testing.T) {
	h, r, caller, p := fixture(t, 7)
	info := netsec.NewSiginfo(int(syscall.SIGTERM), netsec.SI_QUEUE)

	if err := r.Send(p, caller, int(syscall.SIGTERM), info, 0); err != nil {
		t.Fatalf("valid siginfo denied: %v", err)
	}
	call, _ := h.LastSignal()
	if call.Siginfo == nil || call.Siginfo.Code != netsec.SI_QUEUE {
		t.Fatalf("siginfo was not forwarded: %+v", call.Siginfo)
	}
}

func TestSiginfoSignoMustMatch(t *testing.T) {
	h, r, caller, p := fixture(t, 7)
	info := netsec.NewSiginfo(int(syscall.SIGUSR1), netsec.SI_QUEUE)

	err := r.Send(p, caller, int(syscall.SIGTERM), info, 0)
	wantErr(t, err, netsec.ErrInvalidSiginfo, syscall.EINVAL)
	if h.SignalCount() != 0 {
		t.Fatal("mismatched si_signo reached the host")
	}
}

func TestSelfMayUseNonNegativeSiginfo(t *testing.T) {
	h, r, caller, p := fixture(t, 7)
	caller.PID = targetPID
	info := netsec.NewSiginfo(int(syscall.SIGTERM), netsec.SI_USER)

	if err := r.Send(p, caller, int(syscall.SIGTERM), info, 0); err != nil {
		t.Fatalf("self signal denied: %v", err)
	}
	if h.SignalCount() != 1 {
		t.Fatal("self signal was not delivered")
	}
}

func TestNonZeroFlagsAreRejected(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	err := r.Send(p, caller, int(syscall.SIGTERM), nil, 1)
	wantErr(t, err, netsec.ErrInvalidFlags, syscall.EINVAL)
	if h.SignalCount() != 0 {
		t.Fatal("signal with non-zero flags reached the host")
	}
}

func TestUnknownAndClosedPIDFD(t *testing.T) {
	_, r, caller, p := fixture(t, 7)

	wantErr(t, r.Authorize(nil, caller, int(syscall.SIGTERM), nil, 0),
		netsec.ErrPIDFDNotBound, syscall.EBADF)
	wantErr(t, r.Authorize(&netsec.PIDFD{}, caller, int(syscall.SIGTERM), nil, 0),
		netsec.ErrPIDFDNotBound, syscall.EBADF)
	wantErr(t, netsec.AuthorizeSendSignal(nil, caller, int(syscall.SIGTERM), nil, 0),
		netsec.ErrPIDFDNotBound, syscall.EBADF)

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantErr(t, r.Send(p, caller, int(syscall.SIGTERM), nil, 0),
		netsec.ErrPIDFDClosed, syscall.EBADF)
}

func TestUnregisteredFDIsRejected(t *testing.T) {
	h, r, caller, _ := fixture(t, 7)

	err := r.SendFD(caller, 8, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrPIDFDNotBound, syscall.EBADF)

	// A registered fd is tied to the creator's fd table.
	other := caller
	other.PID++
	err = r.SendFD(other, 7, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrPIDFDNotBound, syscall.EBADF)
	if h.SignalCount() != 0 {
		t.Fatal("unregistered fd reached the host")
	}
}

func TestRecycledFileDescriptorIsRejected(t *testing.T) {
	h, r, caller, _ := fixture(t, 7)
	h.SetPIDFD(7, netsec.PIDFDIdentity{Device: 1, Inode: 10})

	err := r.SendFD(caller, 7, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrPIDFDChanged, syscall.EBADF)
	if h.SignalCount() != 0 {
		t.Fatal("recycled fd number reached the host")
	}
}

func TestPIDFDForDifferentTargetIsRejected(t *testing.T) {
	h, r, caller, _ := fixture(t, 7)
	h.SetPIDFDTarget(7, targetPID+1)

	err := r.SendFD(caller, 7, int(syscall.SIGTERM), nil, 0)
	wantErr(t, err, netsec.ErrPIDFDChanged, syscall.EBADF)
	if h.SignalCount() != 0 {
		t.Fatal("pidfd referring to another PID reached the host")
	}
}

func TestRebindingFDInvalidatesOldHandle(t *testing.T) {
	h, r, caller, old := fixture(t, 7)
	h.AddProcess(netsec.ProcessInfo{
		PID:          targetPID + 1,
		StartTime:    1,
		UID:          targetUID,
		PIDNamespace: nsA,
	})
	h.SetPIDFD(7, netsec.PIDFDIdentity{Device: 1, Inode: 11})

	fresh, err := r.Bind(caller, 7, targetPID+1)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	wantErr(t, r.Send(old, caller, int(syscall.SIGTERM), nil, 0),
		netsec.ErrPIDFDClosed, syscall.EBADF)
	if err := r.SendFD(caller, 7, int(syscall.SIGTERM), nil, 0); err != nil {
		t.Fatalf("new binding failed: %v", err)
	}
	if h.SignalCount() != 1 {
		t.Fatal("new binding did not deliver exactly once")
	}
	_ = fresh
}

func TestAuthorizeDoesNotDeliver(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	if err := r.Authorize(p, caller, int(syscall.SIGTERM), nil, 0); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if h.SignalCount() != 0 {
		t.Fatal("Authorize delivered a signal")
	}
}

func TestFlexibleCompatibilityEntryPoint(t *testing.T) {
	h, r, caller, p := fixture(t, 7)

	if err := netsec.Signal(p, int(syscall.SIGTERM), caller, (*netsec.Siginfo)(nil), uint32(0)); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if err := netsec.SendSignal(p, int(syscall.SIGUSR1), (*netsec.Siginfo)(nil), uint32(0), caller); err != nil {
		t.Fatalf("SendSignal signal-first: %v", err)
	}
	if err := p.SendSignal(caller, int(syscall.SIGUSR2), nil, 0); err != nil {
		t.Fatalf("SendSignal caller-first: %v", err)
	}
	if h.SignalCount() != 3 || r == nil {
		t.Fatalf("compatibility entry points delivered %d signals, want 3", h.SignalCount())
	}
}
