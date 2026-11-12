//go:build linux

package netsec

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ProcFS is the production Proc implementation backed by /proc/self.
type ProcFS struct {
	// Dir overrides /proc/self/fd; empty uses the real path.
	Dir string
}

func (p ProcFS) dir() string {
	if p.Dir != "" {
		return p.Dir
	}
	return "/proc/self/fd"
}

func (p ProcFS) ListFDs() ([]int, error) {
	ents, err := os.ReadDir(p.dir())
	if err != nil {
		return nil, err
	}
	fds := make([]int, 0, len(ents))
	for _, e := range ents {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fds = append(fds, n)
	}
	return fds, nil
}

func (p ProcFS) Readlink(fd int) (string, error) {
	return os.Readlink(fmt.Sprintf("%s/%d", p.dir(), fd))
}

func (p ProcFS) Flags(fd int) (uint64, error) {
	info, err := os.ReadFile(fmt.Sprintf("%sinfo/%d", p.dir(), fd))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(info), "\n") {
		if rest, ok := strings.CutPrefix(line, "flags:"); ok {
			return strconv.ParseUint(strings.TrimSpace(rest), 8, 64)
		}
	}
	return 0, fmt.Errorf("no flags line in fdinfo for fd %d", fd)
}

func (p ProcFS) Stat(fd int) (StatInfo, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return StatInfo{}, err
	}
	return StatInfo{
		Mode: modeFromLinux(st.Mode),
		Dev:  uint64(st.Dev),
		Rdev: uint64(st.Rdev),
	}, nil
}

func (p ProcFS) Close(fd int) error {
	return syscall.Close(fd)
}

func modeFromLinux(m uint32) os.FileMode {
	mode := os.FileMode(m & 0o7777)
	switch m & syscall.S_IFMT {
	case syscall.S_IFDIR:
		mode |= os.ModeDir
	case syscall.S_IFSOCK:
		mode |= os.ModeSocket
	case syscall.S_IFCHR:
		mode |= os.ModeCharDevice
	case syscall.S_IFIFO:
		mode |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		mode |= os.ModeSymlink
	}
	return mode
}

// DeviceRdev returns the st_rdev of a device node such as /dev/null, for
// building Policy.StdRdev.
func DeviceRdev(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Rdev), nil
}
