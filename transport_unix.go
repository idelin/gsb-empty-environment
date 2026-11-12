//go:build darwin || linux

package netsec

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const (
	// maxPayload 是一条逻辑消息正文的接收缓冲上限。
	maxPayload = 1 << 16
	// maxFDs 是一条消息允许携带的最大句柄数；超出会导致内核
	// 截断控制消息，接收方按策略整条拒绝。
	maxFDs = 32
	// fdSize 是内核在 SCM_RIGHTS 中编码单个句柄的字节数。
	fdSize = 4
)

// UnixTransport 在已连接的 Unix 域套接字上实现 Transport。
// 约定对端每次 sendmsg 对应一条逻辑消息；凭据通过 getsockopt
// 从内核获取，绝不解析载荷中的自报身份。
type UnixTransport struct {
	conn *net.UnixConn
}

func NewUnixTransport(conn *net.UnixConn) *UnixTransport {
	return &UnixTransport{conn: conn}
}

func (t *UnixTransport) Recv() (*RawMessage, error) {
	buf := make([]byte, maxPayload)
	oob := make([]byte, syscall.CmsgSpace(fdSize*maxFDs))

	n, oobn, flags, _, err := t.conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, fmt.Errorf("netsec: recvmsg: %w", err)
	}

	raw := &RawMessage{
		Payload:          buf[:n],
		ControlTruncated: flags&syscall.MSG_CTRUNC != 0,
	}

	// 即使控制消息被截断，也尽力解析出已送达的句柄，
	// 交给接收方统一关闭，避免泄漏。
	if oobn > 0 {
		raw.FDs, raw.ControlCreds = parseControl(oob[:oobn])
	}

	creds, err := peerCreds(t.conn)
	if err != nil {
		// 内核凭据不可得：保持 PeerCreds 为 nil，由接收方拒绝。
		return raw, nil
	}
	raw.PeerCreds = creds
	return raw, nil
}

// parseControl 从辅助数据中解析出传入句柄与（Linux 上的）凭据。
func parseControl(oob []byte) (fds []int, creds []Creds) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		// 截断可能使最后一条控制消息不完整；stdlib 在出错时丢弃
		// 已解析部分，因此退回逐条尽力解析以回收句柄。
		return parseControlBestEffort(oob)
	}
	for i := range msgs {
		f, c := parseCmsg(&msgs[i])
		fds = append(fds, f...)
		creds = append(creds, c...)
	}
	return fds, creds
}

// parseControlBestEffort 手工逐条扫描 cmsghdr，跳过损坏的尾部。
// 仅在截断等异常路径上使用，目标是尽可能回收已送达的句柄。
func parseControlBestEffort(oob []byte) (fds []int, creds []Creds) {
	hdrLen := syscall.CmsgLen(0)
	for len(oob) >= hdrLen {
		hdr := *(*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
		length := int(hdr.Len)
		if length < hdrLen || length > len(oob) {
			return fds, creds
		}
		m := syscall.SocketControlMessage{
			Header: hdr,
			Data:   oob[hdrLen:length],
		}
		f, c := parseCmsg(&m)
		fds = append(fds, f...)
		creds = append(creds, c...)
		oob = oob[syscall.CmsgSpace(length-hdrLen):]
	}
	return fds, creds
}

// parseCmsg 解析单条控制消息：SCM_RIGHTS 还原句柄，
// 平台相关的凭据消息交给 parseCredsCmsg。
func parseCmsg(m *syscall.SocketControlMessage) (fds []int, creds []Creds) {
	if m.Header.Level != syscall.SOL_SOCKET {
		return nil, nil
	}
	switch m.Header.Type {
	case syscall.SCM_RIGHTS:
		rights, err := syscall.ParseUnixRights(m)
		if err != nil {
			// 数据长度不足以还原全部句柄时，尽量取回完整的前缀。
			return parseRightsBestEffort(m.Data), nil
		}
		return rights, nil
	default:
		return nil, parseCredsCmsg(m)
	}
}

func parseRightsBestEffort(data []byte) []int {
	n := len(data) / fdSize * fdSize
	if n == 0 {
		return nil
	}
	fds := make([]int, 0, n/fdSize)
	for i := 0; i+fdSize <= n; i += fdSize {
		fds = append(fds, int(binary.LittleEndian.Uint32(data[i:i+fdSize])))
	}
	return fds
}
