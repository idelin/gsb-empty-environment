package netsec

import (
	"errors"
	"fmt"
	"syscall"
)

var (
	// ErrControlTruncated 表示内核报告控制消息（辅助数据）被截断。
	// 此时无法确认传入句柄集合完整，整条消息必须拒绝。
	ErrControlTruncated = errors.New("netsec: control message truncated")
	// ErrMissingCreds 表示内核未附带对端凭据，无法建立可信身份。
	ErrMissingCreds = errors.New("netsec: kernel-attached credentials missing")
	// ErrConflictingCreds 表示同一消息上出现多份互相矛盾的凭据。
	ErrConflictingCreds = errors.New("netsec: conflicting credentials on message")
	// ErrIdentityChanged 表示同一条连接上的对端身份发生变化，
	// 典型原因是发送方退出后进程号被复用（代次不同）。
	ErrIdentityChanged = errors.New("netsec: peer identity changed on connection")
)

// Creds 是内核附加到消息上的对端身份，绝不来自载荷自报。
// Generation 是进程代次（如进程启动时间戳），用于区分进程号复用。
type Creds struct {
	PID        int
	UID        uint32
	Generation uint64
}

func (c Creds) Equal(other Creds) bool {
	return c.PID == other.PID && c.UID == other.UID && c.Generation == other.Generation
}

func (c Creds) String() string {
	return fmt.Sprintf("pid=%d uid=%d gen=%d", c.PID, c.UID, c.Generation)
}

// Message 是一条通过校验的接收消息：正文、内核凭据与传入句柄
// 绑定在同一对象上，调用方无法只取其一。
type Message struct {
	Payload []byte
	Creds   Creds
	FDs     []int

	closeFD func(fd int) error
}

// Close 关闭消息携带的全部句柄。重复调用安全。
func (m *Message) Close() error {
	var first error
	for _, fd := range m.FDs {
		if err := m.closeFDOrDefault(fd); err != nil && first == nil {
			first = err
		}
	}
	m.FDs = nil
	return first
}

func (m *Message) closeFDOrDefault(fd int) error {
	if m.closeFD != nil {
		return m.closeFD(fd)
	}
	return syscall.Close(fd)
}

// RawMessage 是传输层一次接收的原始结果。PeerCreds 必须由传输层
// 从内核获取（如 getsockopt），为 nil 表示内核未提供凭据。
type RawMessage struct {
	Payload []byte
	// FDs 是随控制消息传入的句柄；即使消息将被拒绝，
	// 传输层也应把已解析出的句柄放在这里以便接收方关闭。
	FDs []int
	// PeerCreds 是内核附加的对端凭据；nil 表示缺失。
	PeerCreds *Creds
	// ControlCreds 是控制消息中携带的凭据（如 Linux SCM_CREDENTIALS），
	// 每一条都已由内核校验，多条之间及与 PeerCreds 之间必须一致。
	ControlCreds []Creds
	// ControlTruncated 为真表示内核截断了控制消息（MSG_CTRUNC）。
	ControlTruncated bool
}

// Transport 抽象一次逻辑消息的接收，由具体协议（Unix 域套接字、
// 测试替身等）实现。
type Transport interface {
	Recv() (*RawMessage, error)
}

// Receiver 在 Transport 之上执行安全策略：不信任载荷自报身份、
// 拒绝凭据缺失或冲突的消息、截断时关闭已收到句柄、并把连接绑定到
// 首个消息的内核身份以检测进程号复用。
type Receiver struct {
	transport Transport
	// CloseFD 用于关闭句柄，默认 syscall.Close；测试可替换。
	CloseFD func(fd int) error

	bound    *Creds
	bindDone bool
}

func NewReceiver(t Transport) *Receiver {
	return &Receiver{transport: t}
}

// Recv 接收并校验一条消息。任何校验失败都会关闭该消息已携带的
// 全部句柄并返回错误，调用方不会看到半成品消息。
func (r *Receiver) Recv() (*Message, error) {
	raw, err := r.transport.Recv()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("netsec: transport returned nil message")
	}

	reject := func(err error) (*Message, error) {
		r.closeAll(raw.FDs)
		return nil, err
	}

	// 控制消息被截断：句柄集合不可信，整条拒绝。
	if raw.ControlTruncated {
		return reject(ErrControlTruncated)
	}

	// 内核凭据缺失：无法建立可信身份，即使正文完整也拒绝。
	if raw.PeerCreds == nil {
		return reject(ErrMissingCreds)
	}
	peer := *raw.PeerCreds

	// 控制消息中的凭据必须与内核凭据一致；多份控制凭据互相
	// 矛盾时，至少有一份与内核凭据冲突，同样在此被拦下。
	for _, c := range raw.ControlCreds {
		if !c.Equal(peer) {
			return reject(fmt.Errorf("%w: control %s vs kernel %s", ErrConflictingCreds, c, peer))
		}
	}

	// 连接级身份绑定：首条消息确立身份，之后任何变化（包括
	// 进程号相同但代次不同的复用）都拒绝。
	if !r.bindDone {
		bound := peer
		r.bound = &bound
		r.bindDone = true
	} else if !r.bound.Equal(peer) {
		return reject(fmt.Errorf("%w: was %s, now %s", ErrIdentityChanged, r.bound, peer))
	}

	return &Message{
		Payload: raw.Payload,
		Creds:   peer,
		FDs:     raw.FDs,
		closeFD: r.closeFD,
	}, nil
}

func (r *Receiver) closeAll(fds []int) {
	for _, fd := range fds {
		_ = r.closeFD(fd)
	}
}

func (r *Receiver) closeFD(fd int) error {
	if r.CloseFD != nil {
		return r.CloseFD(fd)
	}
	return syscall.Close(fd)
}
