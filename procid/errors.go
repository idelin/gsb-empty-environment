package procid

import "errors"

// 策略性拒绝错误。认证中间件应把这些错误一律视为“拒绝调用”，
// 不要向对端返回具体原因，避免泄漏判定依据。
var (
	// ErrInvalidPID 表示 SO_PEERCRED 未提供有效 PID（如内核未填充）。
	ErrInvalidPID = errors.New("procid: invalid peer pid")

	// ErrStatUnparseable 表示 /proc/PID/stat 内容损坏，无法定位字段。
	ErrStatUnparseable = errors.New("procid: malformed /proc/pid/stat")

	// ErrExeDeleted 表示 /proc/PID/exe 指向已被删除/替换的可执行文件。
	ErrExeDeleted = errors.New("procid: peer executable has been deleted or replaced")

	// ErrProcessExited 表示 pidfd 指向的进程已经退出（PID 可能即将被复用）。
	ErrProcessExited = errors.New("procid: peer process has exited")

	// ErrPIDReused 表示当前占用该 PID 的进程与连接建立时不是同一个 task。
	ErrPIDReused = errors.New("procid: pid has been reused by another process")

	// ErrExeChanged 表示当前 exe 与连接建立时记录的路径不一致。
	ErrExeChanged = errors.New("procid: peer executable path changed")

	// ErrBindingClosed 表示绑定句柄已关闭，不能再用于认证。
	ErrBindingClosed = errors.New("procid: binding closed")

	// ErrNotConfigured 表示 Binder 缺少必需的注入接口。
	ErrNotConfigured = errors.New("procid: ProcFS or PidOpener not configured")
)
