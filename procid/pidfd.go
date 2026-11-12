package procid

import "io"

// PeerCred 是 SO_PEERCRED（unix(7)）返回的对端凭据，由内核在连接建立
// 瞬间填充，无法由对端进程伪造。
type PeerCred struct {
	PID int
	UID uint32
	GID uint32
}

// PidFD 是指向某个 task 的稳定句柄（Linux 上即 pidfd_open(2) 的产物）。
// 与 PID 不同，task 退出后句柄仍然指向“那个已经死掉的 task”，
// 新进程复用同一 PID 不会影响已有句柄，因此可用来检测 PID 复用。
type PidFD interface {
	io.Closer

	// Alive 报告句柄指向的 task 是否仍然存活。
	// 生产实现通过 pidfd_send_signal(pidfd, 0) 探测：ESRCH 即已退出。
	Alive() (bool, error)

	// PID 返回打开句柄时的数字 PID，仅用于错误信息与再查 procfs。
	PID() int
}

// PidOpener 抽象 pidfd_open(2)，测试用假实现注入。
type PidOpener interface {
	OpenPidFD(pid int) (PidFD, error)
}
