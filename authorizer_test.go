package netsec

import (
	"errors"
	"sync"
	"syscall"
	"testing"
)

// fakeTable is a controllable ProcessResolver for tests. Mutating its entries
// simulates process exits and PID reuse without touching real processes.
type fakeTable struct {
	mu    sync.Mutex
	procs map[tableKey]Process
}

type tableKey struct {
	ns  *PIDNamespace
	pid int
}

func newFakeTable() *fakeTable {
	return &fakeTable{procs: make(map[tableKey]Process)}
}

func (t *fakeTable) LookupByPID(ns *PIDNamespace, pid int) (Process, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.procs[tableKey{ns, pid}]
	return p, ok
}

func (t *fakeTable) put(p Process) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.procs[tableKey{p.NS, p.PID}] = p
}

func (t *fakeTable) remove(ns *PIDNamespace, pid int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.procs, tableKey{ns, pid})
}

func assertErrno(t *testing.T, name string, got error, want syscall.Errno) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: want errno %v, got %v", name, want, got)
	}
}

func assertAllow(t *testing.T, name string, got error) {
	t.Helper()
	if got != nil {
		t.Fatalf("%s: want allow (nil), got %v", name, got)
	}
}

// TestPIDReuse: a pidfd stays bound to the original incarnation. After the
// target exits and its PID is recycled by a new process, signals through the
// old pidfd must fail with ESRCH instead of reaching the new occupant.
func TestPIDReuse(t *testing.T) {
	ns := NewPIDNamespace("root", nil)
	table := newFakeTable()
	auth := NewAuthorizer(table)

	// Original incarnation: PID 42, start timestamp 1000.
	victim := Process{
		ID: "proc-victim", PID: 42, StartTime: 1000, UID: 1000, NS: ns, Alive: true,
	}
	table.put(victim)

	fd, err := auth.OpenPIDFD(ns, 42)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}

	caller := Caller{UID: 1000, NS: ns, SelfID: "proc-caller"}
	assertAllow(t, "signal before reuse",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0))

	// Victim exits, then PID 42 is handed to an unrelated process with a
	// different start timestamp and a different UID.
	table.remove(ns, 42)
	replacer := Process{
		ID: "proc-replacer", PID: 42, StartTime: 2000, UID: 2000, NS: ns, Alive: true,
	}
	table.put(replacer)

	assertErrno(t, "stale pidfd after PID reuse",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0), syscall.ESRCH)

	// A freshly opened pidfd targets the new incarnation and works normally.
	newFD, err := auth.OpenPIDFD(ns, 42)
	if err != nil {
		t.Fatalf("OpenPIDFD on reused PID: %v", err)
	}
	owner2 := Caller{UID: 2000, NS: ns, SelfID: "proc-owner2"}
	assertAllow(t, "fresh pidfd on reused PID",
		auth.SendSignal(owner2, newFD, int(syscall.SIGTERM), nil, 0))

	// The stale pidfd must still not reach the replacer even with CAP_KILL.
	capCaller := Caller{UID: 1000, CapKill: true, NS: ns, SelfID: "proc-cap"}
	assertErrno(t, "stale pidfd with CAP_KILL",
		auth.SendSignal(capCaller, fd, int(syscall.SIGTERM), nil, 0), syscall.ESRCH)
}

// TestExitedTarget: once the bound process is gone, every send returns ESRCH.
func TestExitedTarget(t *testing.T) {
	ns := NewPIDNamespace("root", nil)
	table := newFakeTable()
	auth := NewAuthorizer(table)

	target := Process{
		ID: "proc-target", PID: 7, StartTime: 55, UID: 1000, NS: ns, Alive: true,
	}
	table.put(target)

	fd, err := auth.OpenPIDFD(ns, 7)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}
	caller := Caller{UID: 1000, NS: ns, SelfID: "proc-caller"}

	assertAllow(t, "signal while alive",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0))

	// Exit with the PID not yet reused: lookup simply misses.
	table.remove(ns, 7)
	assertErrno(t, "exited, PID free",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0), syscall.ESRCH)

	// Exit represented as a still-present but dead process entry.
	target.Alive = false
	table.put(target)
	assertErrno(t, "exited, marked dead",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0), syscall.ESRCH)

	// Opening a pidfd for a dead PID is itself ESRCH.
	if _, err := auth.OpenPIDFD(ns, 7); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("OpenPIDFD on dead PID: want ESRCH, got %v", err)
	}
}

// TestNamespaceVisibility: the target must be visible from the caller's PID
// namespace. Following Linux semantics a namespace sees itself and every
// descendant namespace; siblings are isolated and an inner namespace cannot
// see processes in an outer namespace. Invisible targets yield ESRCH.
func TestNamespaceVisibility(t *testing.T) {
	rootNS := NewPIDNamespace("root", nil)
	childA := NewPIDNamespace("childA", rootNS)
	childB := NewPIDNamespace("childB", rootNS)
	grandA := NewPIDNamespace("grandA", childA)

	table := newFakeTable()
	auth := NewAuthorizer(table)

	target := Process{
		ID: "proc-target", PID: 9, StartTime: 90, UID: 1000, NS: childA, Alive: true,
	}
	table.put(target)
	fd, err := auth.OpenPIDFD(childA, 9)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}

	sig := int(syscall.SIGTERM)

	// Caller inside the same namespace: visible.
	sameNS := Caller{UID: 1000, NS: childA, SelfID: "proc-same"}
	assertAllow(t, "same namespace", auth.SendSignal(sameNS, fd, sig, nil, 0))

	// Sibling namespace cannot see the target's PID.
	sibling := Caller{UID: 1000, CapKill: true, NS: childB, SelfID: "proc-sibling"}
	assertErrno(t, "sibling namespace",
		auth.SendSignal(sibling, fd, sig, nil, 0), syscall.ESRCH)

	// Inner namespace cannot see processes of an outer (ancestor) namespace.
	grand := Caller{UID: 1000, CapKill: true, NS: grandA, SelfID: "proc-grand"}
	assertErrno(t, "inner namespace cannot see ancestor target",
		auth.SendSignal(grand, fd, sig, nil, 0), syscall.ESRCH)

	// Root namespace is an ancestor of childA, so the target is visible from
	// it; permission then depends on UID/CAP_KILL rather than namespace.
	rootCaller := Caller{UID: 1000, NS: rootNS, SelfID: "proc-root"}
	assertAllow(t, "ancestor namespace", auth.SendSignal(rootCaller, fd, sig, nil, 0))

	// Reverse direction: a process inside an inner namespace IS visible from
	// its outer ancestor namespaces (a host can see container processes);
	// only the inward direction (inner -> outer) and siblings are hidden.
	inner := Process{
		ID: "proc-inner", PID: 9, StartTime: 91, UID: 1000, NS: grandA, Alive: true,
	}
	table.put(inner)
	innerFD, err := auth.OpenPIDFD(grandA, 9)
	if err != nil {
		t.Fatalf("OpenPIDFD inner: %v", err)
	}
	outerFromChild := Caller{UID: 1000, CapKill: true, NS: childA, SelfID: "proc-child"}
	assertAllow(t, "outer child sees inner target",
		auth.SendSignal(outerFromChild, innerFD, sig, nil, 0))
	outerFromRoot := Caller{UID: 1000, CapKill: true, NS: rootNS, SelfID: "proc-root2"}
	assertAllow(t, "root sees inner target",
		auth.SendSignal(outerFromRoot, innerFD, sig, nil, 0))
	assertAllow(t, "same inner namespace sees its target",
		auth.SendSignal(grand, innerFD, sig, nil, 0))
}

// TestSiginfoSpoofing: for non-self targets, non-negative si_code and SI_TKILL
// are rejected, si_signo must match sig, and self signals stay unrestricted.
func TestSiginfoSpoofing(t *testing.T) {
	ns := NewPIDNamespace("root", nil)
	table := newFakeTable()
	auth := NewAuthorizer(table)

	target := Process{
		ID: "proc-target", PID: 5, StartTime: 11, UID: 1000, NS: ns, Alive: true,
	}
	table.put(target)
	fd, err := auth.OpenPIDFD(ns, 5)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}

	sig := int(syscall.SIGTERM)
	caller := Caller{UID: 1000, NS: ns, SelfID: "proc-caller"}

	// si_code >= 0 (SI_USER etc.): rejected.
	assertErrno(t, "SI_USER on non-self",
		auth.SendSignal(caller, fd, sig, &SigInfo{Signo: sig, Code: siUser}, 0),
		syscall.EINVAL)
	assertErrno(t, "positive si_code on non-self",
		auth.SendSignal(caller, fd, sig, &SigInfo{Signo: sig, Code: 6}, 0),
		syscall.EINVAL)

	// SI_TKILL: rejected for a non-self target.
	assertErrno(t, "SI_TKILL on non-self",
		auth.SendSignal(caller, fd, sig, &SigInfo{Signo: sig, Code: siTkill}, 0),
		syscall.EINVAL)

	// Genuine negative kernel-generated codes other than SI_TKILL: allowed.
	assertAllow(t, "SI_QUEUE on non-self",
		auth.SendSignal(caller, fd, sig, &SigInfo{Signo: sig, Code: siQueue}, 0))
	assertAllow(t, "SI_KERNEL on non-self",
		auth.SendSignal(caller, fd, sig, &SigInfo{Signo: sig, Code: siKernel}, 0))

	// si_signo must equal the requested signal, even with an acceptable code.
	assertErrno(t, "si_signo mismatch",
		auth.SendSignal(caller, fd, sig,
			&SigInfo{Signo: int(syscall.SIGKILL), Code: siQueue}, 0),
		syscall.EINVAL)

	// NULL siginfo: allowed.
	assertAllow(t, "nil siginfo", auth.SendSignal(caller, fd, sig, nil, 0))

	// Self-directed signals are exempt: SI_TKILL and SI_USER are normal for
	// tgkill/raise against oneself.
	self := Caller{UID: 1000, NS: ns, SelfID: "proc-target"}
	assertAllow(t, "SI_TKILL on self",
		auth.SendSignal(self, fd, sig, &SigInfo{Signo: sig, Code: siTkill}, 0))
	assertAllow(t, "SI_USER on self",
		auth.SendSignal(self, fd, sig, &SigInfo{Signo: sig, Code: siUser}, 0))
}

// TestPermissions: same-UID callers pass, different UIDs need CAP_KILL, and a
// pidfd stolen from another task confers no extra rights.
func TestPermissions(t *testing.T) {
	ns := NewPIDNamespace("root", nil)
	table := newFakeTable()
	auth := NewAuthorizer(table)

	target := Process{
		ID: "proc-target", PID: 8, StartTime: 33, UID: 1000, NS: ns, Alive: true,
	}
	table.put(target)
	fd, err := auth.OpenPIDFD(ns, 8)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}
	sig := int(syscall.SIGTERM)

	sameUser := Caller{UID: 1000, NS: ns, SelfID: "proc-other-same-uid"}
	assertAllow(t, "same UID allow", auth.SendSignal(sameUser, fd, sig, nil, 0))

	otherUser := Caller{UID: 2000, NS: ns, SelfID: "proc-other-uid"}
	assertErrno(t, "different UID deny",
		auth.SendSignal(otherUser, fd, sig, nil, 0), syscall.EPERM)

	capCaller := Caller{UID: 2000, CapKill: true, NS: ns, SelfID: "proc-cap"}
	assertAllow(t, "CAP_KILL allow", auth.SendSignal(capCaller, fd, sig, nil, 0))

	self := Caller{UID: 9999, NS: ns, SelfID: "proc-target"}
	assertAllow(t, "self always allowed", auth.SendSignal(self, fd, sig, nil, 0))
}

// TestArguments: flags must be zero, sig must be a valid real signal, and
// unknown/closed pidfds yield EBADF.
func TestArguments(t *testing.T) {
	ns := NewPIDNamespace("root", nil)
	table := newFakeTable()
	auth := NewAuthorizer(table)

	target := Process{
		ID: "proc-target", PID: 3, StartTime: 77, UID: 1000, NS: ns, Alive: true,
	}
	table.put(target)
	fd, err := auth.OpenPIDFD(ns, 3)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}
	caller := Caller{UID: 1000, NS: ns, SelfID: "proc-caller"}

	assertErrno(t, "non-zero flags",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 1), syscall.EINVAL)
	assertErrno(t, "sig 0",
		auth.SendSignal(caller, fd, 0, nil, 0), syscall.EINVAL)
	assertErrno(t, "sig too large",
		auth.SendSignal(caller, fd, MaxSignal+1, nil, 0), syscall.EINVAL)
	assertErrno(t, "unknown pidfd",
		auth.SendSignal(caller, fd+999, int(syscall.SIGTERM), nil, 0), syscall.EBADF)

	auth.Close(fd)
	assertErrno(t, "closed pidfd",
		auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0), syscall.EBADF)
}

// TestConcurrent exercises the registry under parallel open/send/close traffic
// to confirm the locking is race-free.
func TestConcurrent(t *testing.T) {
	ns := NewPIDNamespace("root", nil)
	table := newFakeTable()
	auth := NewAuthorizer(table)
	table.put(Process{
		ID: "proc-target", PID: 1, StartTime: 1, UID: 1000, NS: ns, Alive: true,
	})

	fd, err := auth.OpenPIDFD(ns, 1)
	if err != nil {
		t.Fatalf("OpenPIDFD: %v", err)
	}
	caller := Caller{UID: 1000, NS: ns, SelfID: "proc-caller"}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				switch j % 4 {
				case 0:
					_ = auth.SendSignal(caller, fd, int(syscall.SIGTERM), nil, 0)
				case 1:
					newFD, err := auth.OpenPIDFD(ns, 1)
					if err == nil {
						auth.Close(newFD)
					}
				case 2:
					auth.Close(fd + 10000)
				default:
					_ = auth.SendSignal(caller, fd, 0, nil, 0)
				}
			}
		}(i)
	}
	wg.Wait()
}
