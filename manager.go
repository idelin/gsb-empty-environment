package netsec

import (
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"time"
)

// 默认参数。
const (
	// DefaultPSNWindow：期望 PSN 前后各允许的序号跨度，默认 ±64。
	DefaultPSNWindow = 64
	// DefaultErrorThreshold：PSN 越窗丢弃累计多少次后将 QP 置为错误态。
	DefaultErrorThreshold = 8
	// DefaultQuarantine：QPN 销毁后必须经过的隔离（静默）期。
	DefaultQuarantine = 60 * time.Second
)

// 准入与生命周期相关的哨兵错误。
var (
	// ErrQPNNotFound：QPN 没有对应存活 QP。
	ErrQPNNotFound = errors.New("netsec: QPN 不存在")
	// ErrQPNotConnected：QP 尚未建立连接，无法执行该操作。
	ErrQPNotConnected = errors.New("netsec: QP 未处于连接态")
	// ErrQPNQuarantined：QPN 仍在隔离期内，不能重新分配。
	ErrQPNQuarantined = errors.New("netsec: QPN 处于隔离期")
	// ErrQPNExhausted：24 位 QPN 空间（除保留号 0）已用尽。
	ErrQPNExhausted = errors.New("netsec: QPN 空间已耗尽")
	// ErrInvalidPSN：起始 PSN 超出 24 位范围。
	ErrInvalidPSN = errors.New("netsec: PSN 超出 24 位范围")
)

// Clock 抽象时间来源，便于在测试中确定性地推进隔离期。
type Clock func() time.Time

// Config 配置 QP 管理器；零值字段由 NewManager 填充为默认值。
type Config struct {
	// PSNWindow 为接收窗口半宽，窗口为 [expected-w, expected+w]，须 >= 1。
	PSNWindow int
	// ErrorThreshold 为 PSN 越窗丢弃多少次后将 QP 置为错误态，须 >= 1。
	ErrorThreshold int
	// Quarantine 为 QPN 销毁后的隔离时长，须 > 0。
	Quarantine time.Duration
	// Random 提供随机字节；默认使用 crypto/rand（QPN 必须不可预测）。
	Random io.Reader
	// Now 提供当前时间；默认使用 time.Now。
	Now Clock
}

// quarantineEntry 记录被隔离 QPN 的最早可复用时刻。
type quarantineEntry struct {
	availableAt time.Time
}

// Manager 管理软 RNIC 上的 RC 队列对及其入站准入。
type Manager struct {
	mu          sync.Mutex
	qps         map[uint32]*qp
	quarantine  map[uint32]quarantineEntry
	window      int
	threshold   int
	quarantineD time.Duration
	random      io.Reader
	now         Clock
}

// NewManager 按配置创建管理器，缺省项使用默认值。
func NewManager(cfg Config) *Manager {
	window := cfg.PSNWindow
	if window < 1 {
		window = DefaultPSNWindow
	}
	if window > int(PSNMask/2) {
		window = int(PSNMask / 2)
	}
	threshold := cfg.ErrorThreshold
	if threshold < 1 {
		threshold = DefaultErrorThreshold
	}
	quarantine := cfg.Quarantine
	if quarantine <= 0 {
		quarantine = DefaultQuarantine
	}
	random := cfg.Random
	if random == nil {
		random = rand.Reader
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		qps:         make(map[uint32]*qp),
		quarantine:  make(map[uint32]quarantineEntry),
		window:      window,
		threshold:   threshold,
		quarantineD: quarantine,
		random:      random,
		now:         now,
	}
}

// readQPN 从随机源读出一个 24 位 QPN（3 字节，天然无取模偏置）。
func (m *Manager) readQPN() (uint32, error) {
	var b [3]byte
	if _, err := io.ReadFull(m.random, b[:]); err != nil {
		return 0, err
	}
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]), nil
}

// reapQuarantineLocked 清理隔离期已满的 QPN，使其重新可分配。
func (m *Manager) reapQuarantineLocked(now time.Time) {
	for qpn, entry := range m.quarantine {
		if !now.Before(entry.availableAt) {
			delete(m.quarantine, qpn)
		}
	}
}

// AllocateQP 随机分配一个未被占用、未被隔离的 QPN，并创建 RESET 态 QP。
func (m *Manager) AllocateQP() (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	m.reapQuarantineLocked(now)

	// 先做若干次纯随机尝试，保持 QPN 分布不可预测。
	for range 32 {
		qpn, err := m.readQPN()
		if err != nil {
			return 0, err
		}
		if qpn < MinQPN {
			continue
		}
		if _, live := m.qps[qpn]; live {
			continue
		}
		if _, held := m.quarantine[qpn]; held {
			continue
		}
		m.qps[qpn] = &qp{
			qpn:            qpn,
			state:          StateReset,
			psnWindow:      m.window,
			errorThreshold: m.threshold,
		}
		return qpn, nil
	}

	// 随机尝试失败（空间将满或碰撞集中）时退化为线性扫描兜底。
	start, err := m.readQPN()
	if err != nil {
		return 0, err
	}
	for offset := uint32(0); offset <= QPNMask; offset++ {
		qpn := (start + offset) & QPNMask
		if qpn < MinQPN {
			continue
		}
		if _, live := m.qps[qpn]; live {
			continue
		}
		if _, held := m.quarantine[qpn]; held {
			continue
		}
		m.qps[qpn] = &qp{
			qpn:            qpn,
			state:          StateReset,
			psnWindow:      m.window,
			errorThreshold: m.threshold,
		}
		return qpn, nil
	}
	return 0, ErrQPNExhausted
}

// Connect 在连接建立时登记对端 GID 与起始 PSN，并把 QP 转入连接态。
// 模型将 RTR（可接收）与 RTS（可发送）合并为同一就绪态。
func (m *Manager) Connect(qpn uint32, peer GID, startPSN uint32) error {
	if startPSN > PSNMask {
		return ErrInvalidPSN
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.qps[qpn&QPNMask]
	if !ok {
		return ErrQPNNotFound
	}
	if q.state != StateReset {
		return ErrQPNotConnected
	}
	q.peerGID = peer
	q.expectedPSN = startPSN & PSNMask
	q.state = StateRTR
	return nil
}

// DestroyQP 销毁 QP，并把其 QPN 放入隔离表，隔离期满前不得复用。
func (m *Manager) DestroyQP(qpn uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.qps[qpn&QPNMask]
	if !ok {
		return ErrQPNNotFound
	}
	q.state = StateDestroyed
	delete(m.qps, q.qpn)
	m.quarantine[q.qpn] = quarantineEntry{
		availableAt: m.now().Add(m.quarantineD),
	}
	return nil
}

// Admit 供入站报文处理路径调用，对单个报文执行 QP 准入检查。
// 检查顺序：目的 QPN 命中存活 QP → QP 处于连接态 → 源 GID 匹配登记的对端
// → PSN 落在期望窗口内；任一不通过即丢弃。PSN 越窗累计达到阈值时把 QP
// 置为错误态，此后该 QP 的一切报文均被拒绝。
func (m *Manager) Admit(pkt InboundPacket) Verdict {
	dest := pkt.DestQPN & QPNMask

	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.qps[dest]
	if !ok {
		return Verdict{Accepted: false, Reason: DropQPNNotFound, QPN: dest}
	}
	if q.state != StateRTR {
		return Verdict{Accepted: false, Reason: DropNotConnected, QPN: q.qpn, ExpectedPSN: q.expectedPSN}
	}

	// 第一道身份检查：源 GID 必须等于连接建立时登记的对端 GID。
	if pkt.SourceGID != q.peerGID {
		q.gidViolations++
		return Verdict{Accepted: false, Reason: DropGIDMismatch, QPN: q.qpn, ExpectedPSN: q.expectedPSN}
	}

	psn := pkt.PSN & PSNMask
	delta := psnDelta(psn, q.expectedPSN)

	// 第二道序号检查：PSN 必须落在 [expected-w, expected+w] 的回绕窗口内。
	if delta < -int32(q.psnWindow) || delta > int32(q.psnWindow) {
		q.psnMisses++
		if q.psnMisses >= q.errorThreshold {
			q.state = StateError
		}
		return Verdict{Accepted: false, Reason: DropPSNOutOfWindow, QPN: q.qpn, ExpectedPSN: q.expectedPSN}
	}

	// 窗口内：delta<0 为合法重传/重复报文；delta==0 为按序到达，推进期望 PSN；
	// delta>0 为乱序到达，接收但不改变期望 PSN（等待缺失序号补齐）。
	duplicate := delta < 0
	if delta == 0 {
		q.expectedPSN = (q.expectedPSN + 1) & PSNMask
	}
	return Verdict{
		Accepted:    true,
		Reason:      DropNone,
		QPN:         q.qpn,
		Duplicate:   duplicate,
		ExpectedPSN: q.expectedPSN,
	}
}

// Inspect 返回 QP 的只读状态快照；QP 不存在时返回 ErrQPNNotFound。
func (m *Manager) Inspect(qpn uint32) (QPInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, ok := m.qps[qpn&QPNMask]
	if !ok {
		return QPInfo{}, ErrQPNNotFound
	}
	return q.info(), nil
}

// QuarantinedUntil 返回被隔离 QPN 的最早可复用时刻；未被隔离时返回 false。
// 主要供测试与运维观测使用。
func (m *Manager) QuarantinedUntil(qpn uint32) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.quarantine[qpn&QPNMask]
	if !ok {
		return time.Time{}, false
	}
	return entry.availableAt, true
}
