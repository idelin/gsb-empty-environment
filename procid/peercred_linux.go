//go:build linux

package procid

import (
	"fmt"
	"net"
	"syscall"
)

// PeerCredOf 通过 SO_PEERCRED 读取 Unix 域套接字对端凭据。
// 该结构由内核在 connect(2)/accept(2) 时填充，用户态无法伪造。
// 必须在 Accept 后立即调用，不要等处理完业务逻辑再取。
func PeerCredOf(conn *net.UnixConn) (PeerCred, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return PeerCred{}, fmt.Errorf("procid: get raw conn: %w", err)
	}

	var ucred *syscall.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		ucred, sockErr = syscall.GetsockoptUcred(
			int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return PeerCred{}, fmt.Errorf("procid: raw control: %w", err)
	}
	if sockErr != nil {
		return PeerCred{}, fmt.Errorf("procid: SO_PEERCRED: %w", sockErr)
	}

	return PeerCred{PID: int(ucred.Pid), UID: ucred.Uid, GID: ucred.Gid}, nil
}

// BindConn 是守护进程 Accept 循环里的便捷入口：先取 SO_PEERCRED，
// 再建立进程身份绑定。
func (b *Binder) BindConn(conn *net.UnixConn) (*Binding, error) {
	cred, err := PeerCredOf(conn)
	if err != nil {
		return nil, err
	}
	return b.Bind(cred)
}
