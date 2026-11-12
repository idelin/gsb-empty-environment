package netsec

import (
	"errors"
	"sync"
	"testing"
)

// fakeProcTable is an in-memory ProcTable: nsID -> pid -> ProcInfo.
type fakeProcTable struct {
	mu    sync.Mutex
	procs map[uint64]map[int]ProcInfo
}

func newFakeProcTable() *fakeProcTable {
	return &fakeProcTable{procs: make(map[uint64]map[int]ProcInfo)}
}

func (f *fakeProcTable) add(nsID uint64, p ProcInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.procs[nsID] == nil {
		f.procs[nsID] = make(map[int]ProcInfo)
	}
	f.procs[nsID][p.PID] = p
}

func (f *fakeProcTable) remove(nsID uint64, pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.procs[nsID], pid)
}

func (f *fakeProcTable) Lookup(pid int, nsID uint64) (ProcInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.procs[nsID][pid]
	return p, ok
}

const nsHost = uint64(1)

func mustOpen(t *testing.T, r *Registry, c Caller, pid int) int {
	t.Helper()
	fd, err := r.Open(c, pid)
	if err != nil {
		t.Fatalf("Open(%d): %v", pid, err)
	}
	return fd
}

// A recycled pid must not receive signals destined for the dead process.
func TestSendSignalPIDReuse(t *testing.T) {
	table := newFakeProcTable()
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 500, UID: 1000})
	r := NewRegistry(table)
	caller := Caller{PID: 10, UID: 1000, NsID: nsHost}

	fd := mustOpen(t, r, caller, 100)

	// Target exits; the pid is recycled by an unrelated process.
	table.remove(nsHost, 100)
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 900, UID: 1000})

	err := r.SendSignal(caller, fd, 15, nil, 0)
	if !errors.Is(err, ErrSearch) {
		t.Fatalf("recycled pid: got %v, want ESRCH", err)
	}
}

// Signaling an exited target yields ESRCH.
func TestSendSignalExitedTarget(t *testing.T) {
	table := newFakeProcTable()
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 500, UID: 1000})
	r := NewRegistry(table)
	caller := Caller{PID: 10, UID: 1000, NsID: nsHost}

	fd := mustOpen(t, r, caller, 100)
	table.remove(nsHost, 100)

	err := r.SendSignal(caller, fd, 15, nil, 0)
	if !errors.Is(err, ErrSearch) {
		t.Fatalf("exited target: got %v, want ESRCH", err)
	}
}

// Targets outside the caller's PID namespace are unreachable.
func TestCrossNamespace(t *testing.T) {
	const nsSandbox = uint64(2)
	table := newFakeProcTable()
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 500, UID: 1000})
	r := NewRegistry(table)

	// Open from a namespace where the target is not visible.
	sandboxCaller := Caller{PID: 4, UID: 1000, NsID: nsSandbox}
	if _, err := r.Open(sandboxCaller, 100); !errors.Is(err, ErrSearch) {
		t.Fatalf("open across namespaces: got %v, want ESRCH", err)
	}

	// A pidfd minted in the host namespace cannot be used from the
	// sandbox namespace, even when an unrelated process there happens to
	// carry the same pid.
	hostCaller := Caller{PID: 10, UID: 1000, NsID: nsHost}
	fd := mustOpen(t, r, hostCaller, 100)
	table.add(nsSandbox, ProcInfo{PID: 100, StartTime: 900, UID: 1000})

	err := r.SendSignal(sandboxCaller, fd, 15, nil, 0)
	if !errors.Is(err, ErrSearch) {
		t.Fatalf("send across namespaces: got %v, want ESRCH", err)
	}
}

// si_code toward another process must be a negative user-space value other
// than SI_TKILL, and si_signo must match the signal argument.
func TestForgedSiCode(t *testing.T) {
	table := newFakeProcTable()
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 500, UID: 1000})
	r := NewRegistry(table)
	caller := Caller{PID: 10, UID: 1000, NsID: nsHost}
	fd := mustOpen(t, r, caller, 100)

	cases := []struct {
		name string
		info *Siginfo
		want error
	}{
		{"kernel si_code SI_USER", &Siginfo{Signo: 15, Code: SIUser}, ErrPerm},
		{"positive si_code", &Siginfo{Signo: 15, Code: 2}, ErrPerm},
		{"tkill si_code", &Siginfo{Signo: 15, Code: SITkill}, ErrPerm},
		{"sigqueue si_code", &Siginfo{Signo: 15, Code: SIQueue}, nil},
		{"async si_code", &Siginfo{Signo: 15, Code: SIAsyncNL}, nil},
		{"signo mismatch", &Siginfo{Signo: 9, Code: SIQueue}, ErrInval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.SendSignal(caller, fd, 15, tc.info, 0)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	// The same si_code values are legitimate when signaling oneself.
	self := Caller{PID: 100, UID: 1000, NsID: nsHost}
	if err := r.SendSignal(self, fd, 15, &Siginfo{Signo: 15, Code: SITkill}, 0); err != nil {
		t.Fatalf("self tkill si_code: got %v, want nil", err)
	}
}

// Same-uid callers pass; others need CAP_KILL.
func TestCredentials(t *testing.T) {
	table := newFakeProcTable()
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 500, UID: 1000})
	r := NewRegistry(table)
	sameUser := Caller{PID: 10, UID: 1000, NsID: nsHost}
	fd := mustOpen(t, r, sameUser, 100)

	if err := r.SendSignal(sameUser, fd, 15, nil, 0); err != nil {
		t.Fatalf("same user: got %v, want nil", err)
	}

	other := Caller{PID: 11, UID: 2000, NsID: nsHost}
	if err := r.SendSignal(other, fd, 15, nil, 0); !errors.Is(err, ErrPerm) {
		t.Fatalf("other user: got %v, want EPERM", err)
	}

	privileged := Caller{PID: 12, UID: 2000, HasCapKill: true, NsID: nsHost}
	if err := r.SendSignal(privileged, fd, 15, nil, 0); err != nil {
		t.Fatalf("CAP_KILL: got %v, want nil", err)
	}
}

// flags must be zero; unknown fds and bad signal numbers are rejected.
func TestArgumentValidation(t *testing.T) {
	table := newFakeProcTable()
	table.add(nsHost, ProcInfo{PID: 100, StartTime: 500, UID: 1000})
	r := NewRegistry(table)
	caller := Caller{PID: 10, UID: 1000, NsID: nsHost}
	fd := mustOpen(t, r, caller, 100)

	if err := r.SendSignal(caller, fd, 15, nil, 1); !errors.Is(err, ErrInval) {
		t.Fatalf("nonzero flags: got %v, want EINVAL", err)
	}
	if err := r.SendSignal(caller, 9999, 15, nil, 0); !errors.Is(err, ErrBadF) {
		t.Fatalf("unknown fd: got %v, want EBADF", err)
	}
	if err := r.SendSignal(caller, fd, 65, nil, 0); !errors.Is(err, ErrInval) {
		t.Fatalf("bad signal: got %v, want EINVAL", err)
	}
	if err := r.Close(fd); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := r.SendSignal(caller, fd, 15, nil, 0); !errors.Is(err, ErrBadF) {
		t.Fatalf("closed fd: got %v, want EBADF", err)
	}
}
