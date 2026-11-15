package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// devNull is the device the test policy expects on fds 0-2.
var devNull = StdExpectation{ModeType: ModeCharDevice, Rdev: 0x103}

func testPolicy() Policy {
	return Policy{
		Std:   [3]StdExpectation{devNull, devNull, devNull},
		MaxFD: 64,
		Allow: []AllowRule{{TargetPrefix: "pipe:[", ModeType: ModeFIFO}},
	}
}

type fakeInspector struct {
	fds     []int
	enumErr error
	targets map[int]string
	flags   map[int]int
	stats   map[int]FileStat
}

func (f fakeInspector) Enum() ([]int, error) { return f.fds, f.enumErr }

func (f fakeInspector) Readlink(fd int) (string, error) {
	t, ok := f.targets[fd]
	if !ok {
		return "", fmt.Errorf("no such fd %d", fd)
	}
	return t, nil
}

func (f fakeInspector) Flags(fd int) (int, error) {
	fl, ok := f.flags[fd]
	if !ok {
		return 0, fmt.Errorf("no such fd %d", fd)
	}
	return fl, nil
}

func (f fakeInspector) Fstat(fd int) (FileStat, error) {
	st, ok := f.stats[fd]
	if !ok {
		return FileStat{}, fmt.Errorf("no such fd %d", fd)
	}
	return st, nil
}

type fakeCloser struct{ closed []int }

func (c *fakeCloser) Close(fd int) error {
	c.closed = append(c.closed, fd)
	return nil
}

// stdMaps returns probe data for three healthy standard streams.
func stdMaps() (map[int]string, map[int]int, map[int]FileStat) {
	targets := map[int]string{0: "/dev/null", 1: "/dev/null", 2: "/dev/null"}
	flags := map[int]int{0: 0, 1: 0, 2: 0}
	stats := map[int]FileStat{}
	for fd := 0; fd <= 2; fd++ {
		stats[fd] = FileStat{Mode: ModeCharDevice | 0o666, Rdev: devNull.Rdev}
	}
	return targets, flags, stats
}

func entryFor(rep Report, fd int) (Entry, bool) {
	for _, e := range rep.Entries {
		if e.FD == fd {
			return e, true
		}
	}
	return Entry{}, false
}

func TestDecideOutOfRangeFD(t *testing.T) {
	pol := testPolicy()
	for _, fd := range []int{-3, -1, 64, 65, 1 << 20} {
		if got := pol.Decide(FDInfo{FD: fd}); got != DecisionReject {
			t.Errorf("Decide(fd=%d) = %v, want reject", fd, got)
		}
	}
}

func TestSweepOutOfRangeFromEnum(t *testing.T) {
	targets, flags, stats := stdMaps()
	insp := fakeInspector{
		fds:     []int{0, 1, 2, 4096},
		targets: targets,
		flags:   flags,
		stats:   stats,
	}
	closer := &fakeCloser{}
	rep, err := Sweep(insp, closer, testPolicy())
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("Sweep err = %v, want ErrRejected", err)
	}
	e, ok := entryFor(rep, 4096)
	if !ok || e.Decision != DecisionReject {
		t.Fatalf("fd 4096 entry = %+v (ok=%v), want reject", e, ok)
	}
}

func TestSweepFallbackOnEnumFailure(t *testing.T) {
	targets, flags, stats := stdMaps()
	insp := fakeInspector{
		enumErr: errors.New("procfs unavailable"),
		targets: targets,
		flags:   flags,
		stats:   stats,
	}
	closer := &fakeCloser{}
	rep, err := Sweep(insp, closer, testPolicy())
	if err != nil {
		t.Fatalf("Sweep err = %v, want nil", err)
	}
	if !rep.Fallback || rep.EnumErr == nil {
		t.Fatalf("Fallback=%v EnumErr=%v, want fallback with error", rep.Fallback, rep.EnumErr)
	}
	want := []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
		32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47,
		48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63}
	if !reflect.DeepEqual(closer.closed, want) {
		t.Fatalf("closed = %v, want %v", closer.closed, want)
	}
	for fd := 0; fd <= 2; fd++ {
		e, ok := entryFor(rep, fd)
		if !ok || e.Decision != DecisionKeep {
			t.Errorf("std fd %d entry = %+v (ok=%v), want keep", fd, e, ok)
		}
	}
}

func TestSweepRejectsReplacedStdStreams(t *testing.T) {
	targets, flags, stats := stdMaps()
	// fd 0 became a regular file, fd 1 became a unix socket.
	targets[0] = "/home/user/.ssh/id_rsa"
	stats[0] = FileStat{Mode: ModeRegular | 0o600, Rdev: 0x812}
	targets[1] = "socket:[31337]"
	stats[1] = FileStat{Mode: ModeSocket | 0o777}
	insp := fakeInspector{
		fds:     []int{0, 1, 2},
		targets: targets,
		flags:   flags,
		stats:   stats,
	}
	rep, err := Sweep(insp, &fakeCloser{}, testPolicy())
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("Sweep err = %v, want ErrRejected", err)
	}
	for _, fd := range []int{0, 1} {
		e, ok := entryFor(rep, fd)
		if !ok || e.Decision != DecisionReject {
			t.Errorf("fd %d entry = %+v (ok=%v), want reject", fd, e, ok)
		}
	}
	e, ok := entryFor(rep, 2)
	if !ok || e.Decision != DecisionKeep {
		t.Errorf("fd 2 entry = %+v (ok=%v), want keep", e, ok)
	}
}

func TestSweepKeepsWhitelistedLogPipe(t *testing.T) {
	targets, flags, stats := stdMaps()
	// fd 4: legitimate log pipe, whitelisted. fd 5: smuggled sensitive file.
	targets[4] = "pipe:[4242]"
	flags[4] = 0
	stats[4] = FileStat{Mode: ModeFIFO | 0o600}
	targets[5] = "/etc/shadow"
	flags[5] = 0
	stats[5] = FileStat{Mode: ModeRegular | 0o000, Rdev: 0x812}
	insp := fakeInspector{
		fds:     []int{0, 1, 2, 4, 5},
		targets: targets,
		flags:   flags,
		stats:   stats,
	}
	closer := &fakeCloser{}
	rep, err := Sweep(insp, closer, testPolicy())
	if err != nil {
		t.Fatalf("Sweep err = %v, want nil", err)
	}
	e, ok := entryFor(rep, 4)
	if !ok || e.Decision != DecisionKeep {
		t.Errorf("log pipe entry = %+v (ok=%v), want keep", e, ok)
	}
	e, ok = entryFor(rep, 5)
	if !ok || e.Decision != DecisionClose {
		t.Errorf("/etc/shadow entry = %+v (ok=%v), want close", e, ok)
	}
	if !reflect.DeepEqual(closer.closed, []int{5}) {
		t.Errorf("closed = %v, want [5]", closer.closed)
	}
}

func TestSweepRejectsDangerousDescriptors(t *testing.T) {
	cases := map[string]struct {
		target string
		flags  int
		stat   FileStat
	}{
		"opath directory": {"/etc", FlagOPath | 0o0, FileStat{Mode: ModeDirectory | 0o755, Rdev: 0x812}},
		"unix socket":     {"socket:[999]", 0, FileStat{Mode: ModeSocket | 0o777}},
		"pidfd":           {"anon_inode:[pidfd]", 0, FileStat{Mode: ModeRegular | 0o600}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			targets, flags, stats := stdMaps()
			targets[7] = tc.target
			flags[7] = tc.flags
			stats[7] = tc.stat
			insp := fakeInspector{
				fds:     []int{0, 1, 2, 7},
				targets: targets,
				flags:   flags,
				stats:   stats,
			}
			closer := &fakeCloser{}
			// Even a match-all whitelist must not save a dangerous fd.
			pol := testPolicy()
			pol.Allow = append(pol.Allow, AllowRule{})
			rep, err := Sweep(insp, closer, pol)
			if !errors.Is(err, ErrRejected) {
				t.Fatalf("Sweep err = %v, want ErrRejected", err)
			}
			e, ok := entryFor(rep, 7)
			if !ok || e.Decision != DecisionReject {
				t.Fatalf("entry = %+v (ok=%v), want reject", e, ok)
			}
			if len(closer.closed) != 0 {
				t.Errorf("closed = %v, want none", closer.closed)
			}
		})
	}
}
