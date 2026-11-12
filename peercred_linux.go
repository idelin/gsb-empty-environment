//go:build linux

package netsec

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// peerCreds 通过 SO_PEERCRED 从内核读取对端在 connect 时定格的
// 用户与进程号，并用 /proc 中的进程启动时间作为代次，
// 使发送方退出后进程号被复用的情况可以被识别。
func peerCreds(conn *net.UnixConn) (*Creds, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("netsec: syscall conn: %w", err)
	}

	var creds *Creds
	var inner error
	if err := raw.Control(func(fd uintptr) {
		creds, inner = peerCredsFd(int(fd))
	}); err != nil {
		return nil, err
	}
	return creds, inner
}

func peerCredsFd(fd int) (*Creds, error) {
	ucred, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return nil, fmt.Errorf("netsec: getsockopt SO_PEERCRED: %w", err)
	}
	gen, err := processGeneration(int(ucred.Pid))
	if err != nil {
		return nil, err
	}
	return &Creds{PID: int(ucred.Pid), UID: ucred.Uid, Generation: gen}, nil
}

// processGeneration 返回进程实例的代次：/proc/<pid>/stat 的
// starttime（第 22 字段，时钟滴答）。进程号复用时新进程的
// 启动时间不同，代次随之改变。
func processGeneration(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("netsec: read proc stat(%d): %w", pid, err)
	}
	// comm 字段可含空格与括号，从最后一个 ')' 之后切分。
	rest := string(data)[strings.LastIndexByte(string(data), ')')+1:]
	fields := strings.Fields(rest)
	// rest 从第 3 字段（state）开始，starttime 是第 22 字段。
	const starttimeIdx = 22 - 3
	if len(fields) <= starttimeIdx {
		return 0, fmt.Errorf("netsec: proc stat(%d): too few fields", pid)
	}
	start, err := strconv.ParseUint(fields[starttimeIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("netsec: proc stat(%d): %w", pid, err)
	}
	return start, nil
}

// parseCredsCmsg 解析 SCM_CREDENTIALS 控制消息。内容已由内核
// 校验（发送方需特权才能伪造他人身份），这里补齐代次后交由
// 接收方与 SO_PEERCRED 结果交叉比对。
func parseCredsCmsg(m *syscall.SocketControlMessage) []Creds {
	if m.Header.Type != syscall.SCM_CREDENTIALS {
		return nil
	}
	ucred, err := syscall.ParseUnixCredentials(m)
	if err != nil {
		return nil
	}
	gen, err := processGeneration(int(ucred.Pid))
	if err != nil {
		// 进程已退出时无法取代次；置 0 使其与内核凭据冲突而被拒。
		gen = 0
	}
	return []Creds{{PID: int(ucred.Pid), UID: ucred.Uid, Generation: gen}}
}
