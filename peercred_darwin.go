//go:build darwin

package netsec

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const (
	// xnu bsd/sys/socket.h
	solLocal       = 0
	localPeerCred  = 0x001
	localPeerPID   = 0x002
	xucredVersion  = 0
	xucredSize     = 76 // u_int version + uid + short ngroups(+pad) + 16*gid
	xucredUIDOff   = 4
	xucredGroupOff = 12

	// xnu bsd/sys/proc_info.h
	procInfoCallPIDInfo = 0x2
	procPIDTBSDInfo     = 3
	procBSDInfoSize     = 136
	procBSDInfoStartOff = 56 // uint64 pbi_start_tvsec, 其后 8 字节为 tvusec
)

// peerCreds 通过 getsockopt 从内核读取对端在 connect 时定格的
// 用户与进程号，并用 proc_pidinfo 的进程启动时间作为代次，
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
	var xucred [xucredSize]byte
	length := uint32(xucredSize)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		solLocal,
		localPeerCred,
		uintptr(unsafe.Pointer(&xucred[0])),
		uintptr(unsafe.Pointer(&length)),
		0,
	)
	if errno != 0 {
		return nil, fmt.Errorf("netsec: getsockopt LOCAL_PEERCRED: %w", errno)
	}
	if length < xucredGroupOff || binary.LittleEndian.Uint32(xucred[0:4]) != xucredVersion {
		return nil, fmt.Errorf("netsec: unexpected xucred (len=%d)", length)
	}
	uid := binary.LittleEndian.Uint32(xucred[xucredUIDOff : xucredUIDOff+4])

	pid, err := getsockoptInt(fd, solLocal, localPeerPID)
	if err != nil {
		return nil, fmt.Errorf("netsec: getsockopt LOCAL_PEERPID: %w", err)
	}

	gen, err := processGeneration(pid)
	if err != nil {
		return nil, err
	}
	return &Creds{PID: pid, UID: uid, Generation: gen}, nil
}

func getsockoptInt(fd, level, name int) (int, error) {
	var value int32
	length := uint32(4)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(level),
		uintptr(name),
		uintptr(unsafe.Pointer(&value)),
		uintptr(unsafe.Pointer(&length)),
		0,
	)
	if errno != 0 {
		return 0, errno
	}
	return int(value), nil
}

// processGeneration 返回进程实例的代次：启动时间（秒+微秒）编码。
// 进程号复用时新进程的启动时间不同，代次随之改变。
func processGeneration(pid int) (uint64, error) {
	var info [procBSDInfoSize]byte
	n, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		procInfoCallPIDInfo,
		uintptr(pid),
		procPIDTBSDInfo,
		0,
		uintptr(unsafe.Pointer(&info[0])),
		procBSDInfoSize,
	)
	if errno != 0 {
		return 0, fmt.Errorf("netsec: proc_pidinfo(%d): %w", pid, errno)
	}
	if n < procBSDInfoStartOff+16 {
		return 0, fmt.Errorf("netsec: proc_pidinfo(%d): short buffer %d", pid, n)
	}
	sec := binary.LittleEndian.Uint64(info[procBSDInfoStartOff : procBSDInfoStartOff+8])
	usec := binary.LittleEndian.Uint64(info[procBSDInfoStartOff+8 : procBSDInfoStartOff+16])
	return sec*1_000_000 + usec, nil
}

// parseCredsCmsg：macOS 不在控制消息中携带凭据（无 SCM_CREDENTIALS），
// 内核凭据一律经 getsockopt 获取。
func parseCredsCmsg(_ *syscall.SocketControlMessage) []Creds {
	return nil
}
