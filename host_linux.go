//go:build linux

package netsec

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	sysPIDFDSendSignal = 424
	sysPIDFDOpen       = 434
)

type osHost struct{}

// NewOSHost returns a Host that reads process state from /proc and delivers
// signals through the real pidfd_send_signal syscall.
func NewOSHost() Host { return osHost{} }

func (osHost) LookupProcess(pid int) (ProcessInfo, error) {
	if pid <= 0 {
		return ProcessInfo{}, ErrProcessNotFound
	}

	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		return ProcessInfo{}, ErrProcessNotFound
	}
	text := string(data)
	closeParen := strings.LastIndexByte(text, ')')
	if closeParen < 0 || closeParen+2 >= len(text) {
		return ProcessInfo{}, ErrProcessNotFound
	}
	fields := strings.Fields(text[closeParen+2:])
	if len(fields) < 20 {
		return ProcessInfo{}, ErrProcessNotFound
	}
	state := fields[0]
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ProcessInfo{}, ErrProcessNotFound
	}

	info := ProcessInfo{
		PID:       pid,
		StartTime: startTime,
		Exited:    state == "Z" || state == "X" || state == "x",
	}

	if uidFields, err := readStatusUIDs(pid); err == nil {
		info.UID, info.EUID, info.SUID = uidFields[0], uidFields[1], uidFields[2]
	}
	if ino, err := namespaceInode(pid, "pid"); err == nil {
		info.PIDNamespace = ino
	}
	if ino, err := namespaceInode(pid, "user"); err == nil {
		info.UserNamespace = ino
	}
	return info, nil
}

func readStatusUIDs(pid int) ([4]uint32, error) {
	var out [4]uint32
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return out, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return out, fmt.Errorf("malformed Uid line")
		}
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 32)
			if err != nil {
				return out, err
			}
			out[i] = uint32(v)
		}
		return out, nil
	}
	return out, fmt.Errorf("Uid line not found")
}

func namespaceInode(pid int, name string) (uint64, error) {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d/ns/%s", pid, name))
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat_t unavailable")
	}
	return uint64(st.Ino), nil
}

func (osHost) CanSee(caller Caller, target ProcessInfo) bool {
	return visibleFrom(caller, target)
}

type kernelSiginfo struct {
	Signo int32
	Errno int32
	Code  int32
	_     [116]byte
}

func (osHost) SendSignal(fd, _ int, sig int, info *Siginfo, flags uint32) error {
	var raw *kernelSiginfo
	if info != nil {
		raw = &kernelSiginfo{
			Signo: int32(info.Signo),
			Errno: int32(info.Errno),
			Code:  int32(info.Code),
		}
	}
	infoPtr := uintptr(0)
	if raw != nil {
		infoPtr = uintptr(unsafe.Pointer(raw))
	}
	_, _, errno := syscall.RawSyscall6(
		sysPIDFDSendSignal,
		uintptr(fd),
		uintptr(sig),
		infoPtr,
		uintptr(flags),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func (osHost) PIDFDIdentity(fd int) (PIDFDIdentity, error) {
	if fd < 0 {
		return PIDFDIdentity{}, syscall.EBADF
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return PIDFDIdentity{}, err
	}
	return PIDFDIdentity{Device: uint64(st.Dev), Inode: st.Ino}, nil
}

func (osHost) PIDFDTarget(fd int) (int, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", fd))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Pid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed Pid line")
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, err
		}
		return pid, nil
	}
	// Kernels before 5.3 do not expose Pid in pidfd fdinfo.
	return 0, nil
}

// PidfdOpen opens a kernel pidfd for pid.  It is provided so a sandbox
// interceptor can create the descriptor it subsequently binds.
func PidfdOpen(pid int) (int, error) {
	r1, _, errno := syscall.RawSyscall(sysPIDFDOpen, uintptr(pid), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(r1), nil
}

// OpenPIDFD implements PIDFDOpener.
func (osHost) OpenPIDFD(pid int) (int, error) { return PidfdOpen(pid) }

// OpenOSPIDFD is an alias for PidfdOpen.
func OpenOSPIDFD(pid int) (int, error) { return PidfdOpen(pid) }
