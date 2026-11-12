//go:build linux

package procid

import (
	"fmt"
	"syscall"
)

// 这两个系统调用在所有 Linux 架构上编号一致（引入时已进入通用编号时代）。
// Go 标准库 syscall 仅在 loong64 的 zsysnum 中提供常量，这里统一自带，
// 并使用独立命名以免与标准库在个别架构上的声明冲突。
const (
	sysPidfdOpen       = 434 // pidfd_open(2)
	sysPidfdSendSignal = 424 // pidfd_send_signal(2)
)

// LinuxPidOpener 通过 pidfd_open(2) 取得 task 稳定句柄。
type LinuxPidOpener struct{}

// DefaultPidOpener 返回生产用 pidfd opener。
func DefaultPidOpener() *LinuxPidOpener { return &LinuxPidOpener{} }

// OpenPidFD 调用 pidfd_open(pid, 0)。打开时设 close-on-exec，
// 避免句柄泄漏给子进程。
func (LinuxPidOpener) OpenPidFD(pid int) (PidFD, error) {
	r1, _, errno := syscall.Syscall(sysPidfdOpen, uintptr(pid), 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("procid: pidfd_open(%d): %w", pid, errno)
	}
	fd := int(r1)
	// pidfd_open 默认不置 CLOEXEC（flags 为 0 时），显式补上。
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd),
		uintptr(syscall.F_SETFD), uintptr(syscall.FD_CLOEXEC)); errno != 0 {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("procid: set CLOEXEC on pidfd: %w", errno)
	}
	return &linuxPidFD{fd: fd, pid: pid}, nil
}

type linuxPidFD struct {
	fd  int
	pid int
}

// Alive 用 pidfd_send_signal(fd, 0, NULL, 0) 探活：信号 0 只做权限与
// 存在性检查。ESRCH 明确表示该 task 已退出；EPERM 表示进程仍在但当前
// 用户无权发信号，仍应视为存活。
func (p *linuxPidFD) Alive() (bool, error) {
	_, _, errno := syscall.Syscall6(
		sysPidfdSendSignal,
		uintptr(p.fd), 0, 0, 0, 0, 0,
	)
	switch errno {
	case 0:
		return true, nil
	case syscall.ESRCH:
		return false, nil
	case syscall.EPERM:
		// 进程存在，只是无权向它发信号；对特权守护进程一般不会走到。
		return true, nil
	default:
		return false, fmt.Errorf("procid: pidfd_send_signal: %w", errno)
	}
}

func (p *linuxPidFD) PID() int { return p.pid }

func (p *linuxPidFD) Close() error { return syscall.Close(p.fd) }
