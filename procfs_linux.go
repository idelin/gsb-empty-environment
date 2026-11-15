//go:build linux

package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ProcFS inspects the current process through procfs. It is the
// production Inspector for Linux privileged helpers.
type ProcFS struct {
	// FDDir overrides /proc/self/fd, mainly for tests.
	FDDir string
}

func (p ProcFS) fdDir() string {
	if p.FDDir != "" {
		return p.FDDir
	}
	return "/proc/self/fd"
}

func (p ProcFS) fdinfoDir() string {
	return filepath.Join(filepath.Dir(p.fdDir()), "fdinfo")
}

// Enum lists open descriptors by reading the fd directory.
func (p ProcFS) Enum() ([]int, error) {
	entries, err := os.ReadDir(p.fdDir())
	if err != nil {
		return nil, err
	}
	fds := make([]int, 0, len(entries))
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fds = append(fds, fd)
	}
	return fds, nil
}

// Readlink resolves the /proc/self/fd symlink for fd.
func (p ProcFS) Readlink(fd int) (string, error) {
	return os.Readlink(filepath.Join(p.fdDir(), strconv.Itoa(fd)))
}

// Flags reads the descriptor status flags from /proc/self/fdinfo.
func (p ProcFS) Flags(fd int) (int, error) {
	raw, err := os.ReadFile(filepath.Join(p.fdinfoDir(), strconv.Itoa(fd)))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "flags:" {
			v, err := strconv.ParseUint(fields[1], 8, 64)
			if err != nil {
				return 0, err
			}
			return int(v), nil
		}
	}
	return 0, errors.New("netsec: no flags line in fdinfo")
}

// Fstat stats the open descriptor.
func (p ProcFS) Fstat(fd int) (FileStat, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return FileStat{}, err
	}
	return FileStat{Mode: st.Mode, Rdev: uint64(st.Rdev)}, nil
}

// SysCloser closes descriptors with close(2).
type SysCloser struct{}

// Close implements Closer.
func (SysCloser) Close(fd int) error { return syscall.Close(fd) }

// ExpectDevice builds a StdExpectation for the device node at path,
// e.g. ExpectDevice("/dev/null") for helpers whose standard streams must
// be redirected to /dev/null.
func ExpectDevice(path string) (StdExpectation, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return StdExpectation{}, err
	}
	return StdExpectation{ModeType: st.Mode & ModeTypeMask, Rdev: uint64(st.Rdev)}, nil
}

// RlimitMaxFD returns RLIMIT_NOFILE capped at AbsoluteMaxFD, a sane
// MaxFD for the fallback sweep.
func RlimitMaxFD() (int, error) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return 0, err
	}
	if rl.Cur > AbsoluteMaxFD {
		return AbsoluteMaxFD, nil
	}
	return int(rl.Cur), nil
}
