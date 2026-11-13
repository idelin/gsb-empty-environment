package netsec

import (
	"errors"
	"math"
	"sync"
)

// RFC 9000 常量。
const (
	// MinInitialDatagramSize 是客户端 Initial 数据报的最小 UDP 载荷
	// 字节数（RFC 9000 第 14.1 节）。小于该值的 Initial 必须丢弃。
	MinInitialDatagramSize = 1200

	// AmplificationFactor 是地址验证完成前允许发送字节数相对
	// 已接收字节数的放大倍数（RFC 9000 第 8.1 节）。
	AmplificationFactor = 3
)

var (
	// ErrBudgetExhausted 表示路径的反放大预算已耗尽，发送（包括
	// PTO 探测包）必须被阻塞。
	ErrBudgetExhausted = errors.New("netsec: anti-amplification budget exhausted")

	// ErrPathChallengeFirst 表示迁移后的新路径尚未发出
	// PATH_CHALLENGE，不允许先发送数据。
	ErrPathChallengeFirst = errors.New("netsec: PATH_CHALLENGE must be sent before data on a migrated path")
)

// ValidationSource 标识地址验证的依据。
type ValidationSource int

const (
	// ValidatedByHandshakeDecryption 表示成功解密了该连接的
	// Handshake 包（RFC 9000 第 8.1 节）。
	ValidatedByHandshakeDecryption ValidationSource = iota

	// ValidatedByRetryToken 表示收到了有效的 Retry 令牌。
	ValidatedByRetryToken

	// ValidatedByNewToken 表示收到了有效的 NEW_TOKEN 令牌。
	ValidatedByNewToken
)

// Path 记录单条网络路径的反放大状态。每条路径独立计数，
// 连接迁移后必须为新路径新建 Path。
type Path struct {
	received  int // 该路径已接收的 UDP 载荷字节数
	sent      int // 该路径已发送的字节数
	validated bool
	source    ValidationSource
}

// NewPath 返回一条未验证、计数为零的新路径。
func NewPath() *Path {
	return &Path{}
}

// OnDatagramReceived 记录该路径收到的一个 UDP 数据报载荷。
// isInitial 为 true 且 size 小于 MinInitialDatagramSize 时，
// 数据报被丢弃且不计入预算，返回 false；否则计入并返回 true。
func (p *Path) OnDatagramReceived(size int, isInitial bool) bool {
	if size <= 0 {
		return false
	}
	if isInitial && size < MinInitialDatagramSize {
		return false
	}
	p.received += size
	return true
}

// Validate 将路径标记为已验证，此后发送不再受预算限制。
func (p *Path) Validate(src ValidationSource) {
	p.validated = true
	p.source = src
}

// Validated 报告路径地址是否已验证。
func (p *Path) Validated() bool {
	return p.validated
}

// Received 返回该路径已接收的 UDP 载荷字节数。
func (p *Path) Received() int {
	return p.received
}

// Sent 返回该路径已发送的字节数。
func (p *Path) Sent() int {
	return p.sent
}

// Budget 返回当前还可发送的字节数。已验证路径不受限。
func (p *Path) Budget() int {
	if p.validated {
		return math.MaxInt
	}
	remaining := AmplificationFactor*p.received - p.sent
	if remaining < 0 {
		return 0
	}
	return remaining
}

// CanSend 报告发送 n 字节是否不超出预算。
func (p *Path) CanSend(n int) bool {
	if n <= 0 {
		return false
	}
	if p.validated {
		return true
	}
	return p.sent+n <= AmplificationFactor*p.received
}

// OnPacketSent 记录一次发送。超出预算时不记账并返回
// ErrBudgetExhausted。PTO 探测包同样受此约束，不得例外。
func (p *Path) OnPacketSent(n int) error {
	if !p.CanSend(n) {
		return ErrBudgetExhausted
	}
	p.sent += n
	return nil
}

// Manager 管理一条连接的当前活动路径，供发送调度调用。
// 连接迁移时通过 Migrate 切换到独立计预算的新路径。
type Manager struct {
	mu            sync.Mutex
	path          *Path
	migrated      bool // 当前路径是否为迁移后的新路径
	challengeSent bool // 迁移后的路径上是否已发出 PATH_CHALLENGE
}

// NewManager 为一条新连接创建管理器，初始路径未验证。
func NewManager() *Manager {
	return &Manager{path: NewPath()}
}

// ReceiveDatagram 在当前路径上记录收到的 UDP 载荷。
// 过小的 Initial 数据报被丢弃且不计入预算，返回 false。
func (m *Manager) ReceiveDatagram(size int, isInitial bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path.OnDatagramReceived(size, isInitial)
}

// Validate 将当前路径标记为已验证。
func (m *Manager) Validate(src ValidationSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.path.Validate(src)
}

// Validated 报告当前路径是否已验证。
func (m *Manager) Validated() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path.Validated()
}

// Budget 返回当前路径剩余可发送字节数。
func (m *Manager) Budget() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path.Budget()
}

// CanSend 报告当前路径是否允许发送 n 字节。
func (m *Manager) CanSend(n int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path.CanSend(n)
}

// NeedsPathChallenge 报告迁移后的新路径是否仍需先发出
// PATH_CHALLENGE。
func (m *Manager) NeedsPathChallenge() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.migrated && !m.challengeSent
}

// Migrate 切换到新路径：预算从零开始独立计数，路径恢复未验证
// 状态，且必须先发送 PATH_CHALLENGE 再发送数据。
func (m *Manager) Migrate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.path = NewPath()
	m.migrated = true
	m.challengeSent = false
}

// SendPathChallenge 在当前路径上记录 PATH_CHALLENGE 数据报的发送，
// 消耗预算。仅在迁移后的路径上有必要调用。
func (m *Manager) SendPathChallenge(size int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.path.OnPacketSent(size); err != nil {
		return err
	}
	m.challengeSent = true
	return nil
}

// Send 记录一次普通数据发送。预算耗尽时返回 ErrBudgetExhausted；
// 迁移后的路径上若尚未发出 PATH_CHALLENGE，返回
// ErrPathChallengeFirst。
func (m *Manager) Send(size int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migrated && !m.challengeSent {
		return ErrPathChallengeFirst
	}
	return m.path.OnPacketSent(size)
}

// SendProbe 记录一次 PTO 探测包发送。探测包与普通发送共用预算，
// 预算耗尽时同样被阻塞，不得突破限制。
func (m *Manager) SendProbe(size int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.path.OnPacketSent(size)
}
