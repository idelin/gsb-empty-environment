package netsec

import (
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
)

const testStdRdev = 0x103 // pretend /dev/null

type fakeFD struct {
	target string
	flags  uint64
	mode   os.FileMode
	rdev   uint64
}

type fakeProc struct {
	fds     map[int]fakeFD
	listErr error
	closed  []int
}

func stdFds() map[int]fakeFD {
	return map[int]fakeFD{
		0: {target: "/dev/null", mode: os.ModeCharDevice, rdev: testStdRdev},
		1: {target: "/dev/null", mode: os.ModeCharDevice, rdev: testStdRdev},
		2: {target: "/dev/null", mode: os.ModeCharDevice, rdev: testStdRdev},
	}
}

func (f *fakeProc) ListFDs() ([]int, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]int, 0, len(f.fds))
	for fd := range f.fds {
		out = append(out, fd)
	}
	sort.Ints(out)
	return out, nil
}

func (f *fakeProc) Readlink(fd int) (string, error) {
	d, ok := f.fds[fd]
	if !ok {
		return "", errors.New("no such fd")
	}
	return d.target, nil
}

func (f *fakeProc) Flags(fd int) (uint64, error) {
	d, ok := f.fds[fd]
	if !ok {
		return 0, errors.New("no such fd")
	}
	return d.flags, nil
}

func (f *fakeProc) Stat(fd int) (StatInfo, error) {
	d, ok := f.fds[fd]
	if !ok {
		return StatInfo{}, errors.New("no such fd")
	}
	return StatInfo{Mode: d.mode, Rdev: d.rdev}, nil
}

func (f *fakeProc) Close(fd int) error {
	f.closed = append(f.closed, fd)
	return nil
}

func decisionOf(t *testing.T, rep *Report, fd int) Decision {
	t.Helper()
	for _, e := range rep.Entries {
		if e.FD == fd {
			return e.Decision
		}
	}
	t.Fatalf("no entry for fd %d", fd)
	return DecisionKeep
}

func TestOutOfRangeFDRejected(t *testing.T) {
	fds := stdFds()
	fds[1<<24] = fakeFD{target: "/etc/shadow"}
	p := &fakeProc{fds: fds}

	rep, err := Audit(p, Policy{StdRdev: testStdRdev})
	if err == nil {
		t.Fatal("expected rejection")
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected *RejectError, got %T", err)
	}
	if got := decisionOf(t, rep, 1<<24); got != DecisionReject {
		t.Fatalf("fd 1<<24: got %v, want reject", got)
	}
	if len(p.closed) != 0 {
		t.Fatalf("out-of-range fd must not be closed close attempted: %v", p.closed)
	}
}

func TestEnumerationFailureFallback(t *testing.T) {
	p := &fakeProc{fds: stdFds(), listErr: errors.New("proc gone")}

	rep, err := Audit(p, Policy{StdRdev: testStdRdev, FallbackMax: 8})
	if err != nil {
		t.Fatalf("fallback with sane std streams must not reject: %v", err)
	}
	if !rep.Fallback {
		t.Fatal("expected Fallback to be set")
	}
	want := []int{3, 4, 5, 6, 7, 8}
	if !reflect.DeepEqual(p.closed, want) {
		t.Fatalf("blind close = %v, want %v", p.closed, want)
	}
	for fd := 0; fd < 3; fd++ {
		if got := decisionOf(t, rep, fd); got != DecisionKeep {
			t.Fatalf("std fd %d: got %v, want keep", fd, got)
		}
	}
}

func TestEnumerationFailureFallbackRejectsBadStd(t *testing.T) {
	fds := stdFds()
	fds[0] = fakeFD{target: "pipe:[9]", mode: os.ModeNamedPipe}
	p := &fakeProc{fds: fds, listErr: errors.New("proc gone")}

	if _, err := Audit(p, Policy{StdRdev: testStdRdev, FallbackMax: 5}); err == nil {
		t.Fatal("replaced stdin must reject even in fallback mode")
	}
}

func TestStdStreamReplaced(t *testing.T) {
	fds := stdFds()
	fds[1] = fakeFD{target: "/home/user/.ssh/id_rsa", mode: 0}
	p := &fakeProc{fds: fds}

	rep, err := Audit(p, Policy{StdRdev: testStdRdev})
	if err == nil {
		t.Fatal("expected rejection when stdout is a regular file")
	}
	if got := decisionOf(t, rep, 1); got != DecisionReject {
		t.Fatalf("fd 1: got %v, want reject", got)
	}
	if got := decisionOf(t, rep, 0); got != DecisionKeep {
		t.Fatalf("fd 0: got %v, want keep", got)
	}
}

func TestStdStreamWrongDevice(t *testing.T) {
	fds := stdFds()
	fds[2] = fakeFD{target: "/dev/pts/7", mode: os.ModeCharDevice, rdev: 0x999}
	p := &fakeProc{fds: fds}

	if _, err := Audit(p, Policy{StdRdev: testStdRdev}); err == nil {
		t.Fatal("expected rejection when stderr is an unexpected device")
	}
}

func TestLogPipeKeptAndStrayClosed(t *testing.T) {
	fds := stdFds()
	fds[3] = fakeFD{target: "pipe:[4242]", mode: os.ModeNamedPipe}
	fds[4] = fakeFD{target: "/var/log/app/old.log", mode: 0}
	p := &fakeProc{fds: fds}

	rep, err := Audit(p, Policy{
		StdRdev:     testStdRdev,
		KeepTargets: []string{"pipe:[4242]"},
	})
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if got := decisionOf(t, rep, 3); got != DecisionKeep {
		t.Fatalf("log pipe: got %v, want keep", got)
	}
	if got := decisionOf(t, rep, 4); got != DecisionClose {
		t.Fatalf("stray file: got %v, want close", got)
	}
	if !reflect.DeepEqual(p.closed, []int{4}) {
		t.Fatalf("closed = %v, want [4]", p.closed)
	}
}

func TestDangerousDescriptorsRejected(t *testing.T) {
	cases := map[string]fakeFD{
		"O_PATH dir":  {target: "/etc", flags: OPath, mode: os.ModeDir},
		"unix socket": {target: "socket:[777]", mode: os.ModeSocket},
		"pidfd":       {target: "anon_inode:[pidfd]", mode: 0},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			fds := stdFds()
			fds[7] = d
			p := &fakeProc{fds: fds}

			rep, err := Audit(p, Policy{StdRdev: testStdRdev})
			if err == nil {
				t.Fatal("expected rejection")
			}
			if got := decisionOf(t, rep, 7); got != DecisionReject {
				t.Fatalf("got %v, want reject", got)
			}
		})
	}
}
