package mutex

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ProcInfo 描述对一个进程的观测结果。
type ProcInfo struct {
	// Alive 表示该 PID 当前是否存活。
	Alive bool
	// StartToken 是进程启动时刻的不透明标识（取自 ps 的 lstart）。
	// PID 被操作系统复用后，新进程的 StartToken 与旧记录不同，
	// 据此可区分"原持有者还活着"与"PID 已被复用"。
	StartToken string
}

// Inspector 是进程观测的注入点。生产实现为 OSInspector，
// 测试可注入伪造实现以模拟进程死亡、失联与 PID 复用。
type Inspector interface {
	Inspect(pid int) ProcInfo
}

// OSInspector 基于 kill(pid, 0) 与 ps(1) 的真实实现（Unix）。
type OSInspector struct{}

func (OSInspector) Inspect(pid int) ProcInfo {
	if pid <= 0 {
		return ProcInfo{}
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return ProcInfo{}
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return ProcInfo{}
	}
	return ProcInfo{Alive: true, StartToken: processStartToken(pid)}
}

// processStartToken 返回进程启动时间字符串，作为不透明 token 使用，
// 只做相等性比较，不解析。取不到时返回空串（调用方按保守策略处理）。
func processStartToken(pid int) string {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
