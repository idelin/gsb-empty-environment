package netsec

import (
	"crypto/x509"
	"errors"
	"math/big"
	"sync"
)

// State 表示单个证书序列号在 CRL 视角下的吊销状态。
type State int

const (
	// Good 表示证书当前未被吊销。从未出现在 CRL 中的序列号也按 Good 处理。
	Good State = iota
	// Held 表示证书被暂挂（certificateHold），属于临时性吊销。
	Held
	// Revoked 表示证书被永久吊销。
	Revoked
)

func (s State) String() string {
	switch s {
	case Held:
		return "held"
	case Revoked:
		return "revoked"
	default:
		return "good"
	}
}

// Reason 对应 RFC 5280 定义的 CRLReason 枚举值。
type Reason int

const (
	ReasonUnspecified          Reason = 0
	ReasonKeyCompromise        Reason = 1
	ReasonCACompromise         Reason = 2
	ReasonAffiliationChanged   Reason = 3
	ReasonSuperseded           Reason = 4
	ReasonCessationOfOperation Reason = 5
	ReasonCertificateHold      Reason = 6
	ReasonRemoveFromCRL        Reason = 8
	ReasonPrivilegeWithdrawn   Reason = 9
	ReasonAACompromise         Reason = 10
)

// Entry 是证书路径（或 CRL 解析层）送来的单条 CRL 记录。
type Entry struct {
	// SerialNumber 是条目的证书序列号，不允许为 nil。
	SerialNumber *big.Int
	// Reason 是条目的吊销原因；未携带原因扩展时填 ReasonUnspecified。
	Reason Reason
}

var (
	// ErrNilSerial 表示条目缺少证书序列号。
	ErrNilSerial = errors.New("netsec: CRL entry has nil serial number")
	// ErrInvalidRemoveFromCRL 表示 removeFromCRL 条目没有对应的暂挂，
	// 即该序列号当前并非 Held，按规则该条目无效、状态保持不变。
	ErrInvalidRemoveFromCRL = errors.New("netsec: removeFromCRL without matching certificateHold is invalid")
)

// Transition 是纯函数状态机：给定序列号的当前状态 current 和送来的 CRL 条目，
// 返回应用该条目后的新状态。
//
// 规则：
//   - certificateHold（暂挂）使任何当前状态变为 Held；永久吊销不可逆，
//     因此 Revoked 上的暂挂仍保持 Revoked。
//   - removeFromCRL（移除）只能解除同一序列号的暂挂：仅当当前状态为 Held 时
//     才回到 Good；否则条目无效（返回 ErrInvalidRemoveFromCRL，状态不变）。
//   - 其他任何原因都属于永久吊销，进入 Revoked 后不可恢复。
func Transition(current State, entry Entry) (State, error) {
	switch entry.Reason {
	case ReasonCertificateHold:
		if current == Revoked {
			return Revoked, nil
		}
		return Held, nil
	case ReasonRemoveFromCRL:
		if current != Held {
			return current, ErrInvalidRemoveFromCRL
		}
		return Good, nil
	default:
		return Revoked, nil
	}
}

// Machine 是可导入的 CRL 吊销状态机，按证书序列号维护状态，
// 接收证书路径（或 CRL 解析层）送来的条目。多个 goroutine 可并发使用。
type Machine struct {
	mu     sync.RWMutex
	states map[string]State
}

// NewMachine 创建一个空状态机。
func NewMachine() *Machine {
	return &Machine{states: make(map[string]State)}
}

func serialKey(serial *big.Int) string {
	return string(serial.Bytes())
}

// State 返回序列号的当前状态；从未记录过的序列号按 Good 处理。
func (m *Machine) State(serial *big.Int) State {
	if serial == nil {
		return Good
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.states[serialKey(serial)]
}

// Apply 应用一条 CRL 条目并返回该序列号应用后的状态。
//
// removeFromCRL 仅在存在同一序列号的暂挂时有效；对从未暂挂（或已永久吊销）
// 的序列号放入移除条目会返回 ErrInvalidRemoveFromCRL，且状态保持不变。
func (m *Machine) Apply(entry Entry) (State, error) {
	if entry.SerialNumber == nil {
		return Good, ErrNilSerial
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := serialKey(entry.SerialNumber)
	next, err := Transition(m.states[key], entry)
	if err != nil {
		return m.states[key], err
	}
	if next == Good {
		delete(m.states, key)
	} else {
		m.states[key] = next
	}
	return next, nil
}

// ApplyRevocationList 应用标准库解析出的 CRL 中的全部条目，
// 按 CRL 中的条目顺序逐条处理；遇到第一个无效条目即停止并返回错误。
func (m *Machine) ApplyRevocationList(rl *x509.RevocationList) error {
	for _, record := range rl.RevokedCertificateEntries {
		entry := Entry{
			SerialNumber: record.SerialNumber,
			Reason:       Reason(record.ReasonCode),
		}
		if _, err := m.Apply(entry); err != nil {
			return err
		}
	}
	return nil
}

// Len 返回当前处于非 Good 状态（暂挂或永久吊销）的序列号数量。
func (m *Machine) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.states)
}

// Reset 清空所有序列号的状态。
func (m *Machine) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = make(map[string]State)
}
