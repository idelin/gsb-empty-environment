package quota

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrQuotaExceeded 预留或结算超出配额；结算超额时文件不得发布。
	ErrQuotaExceeded = errors.New("quota: limit exceeded")
	// ErrReservationExists 预留单号已存在且参数不一致（幂等冲突）。
	ErrReservationExists = errors.New("quota: reservation exists with different parameters")
	// ErrReservationNotFound 预留单不存在。
	ErrReservationNotFound = errors.New("quota: reservation not found")
	// ErrReservationClosed 预留已终结，操作不再适用。
	ErrReservationClosed = errors.New("quota: reservation already closed")
	// ErrSettleConflict 重复回执但结算参数与首次不一致。
	ErrSettleConflict = errors.New("quota: duplicate settle with different parameters")
	// ErrBlockNotFound 物理块不存在。
	ErrBlockNotFound = errors.New("quota: block not found")
	// ErrNotOwner 不是物理块当前归属方。
	ErrNotOwner = errors.New("quota: block owned by another tenant")
	// ErrInvalidArgument 参数非法。
	ErrInvalidArgument = errors.New("quota: invalid argument")
)

// ReservationState 预留单状态。
type ReservationState string

const (
	StateOpen     ReservationState = "open"     // 已预留，待结算/取消
	StateSettled  ReservationState = "settled"  // 已结算
	StateRejected ReservationState = "rejected" // 结算超额被拒，文件未发布
	StateCanceled ReservationState = "canceled" // 已取消
	StateAborted  ReservationState = "aborted"  // 压缩失败等中止
)

// Reservation 一张预留单。预留、结算、取消均以 ID 幂等。
type Reservation struct {
	ID             string
	Tenant         string
	ReservedBytes  int64
	CompressedSize int64 // 首次结算上报的压缩后大小
	BilledBytes    int64 // 实际计费字节（命中共享块去重时为 0）
	BlockID        string
	State          ReservationState
}

// block 共享物理块：只向归属租户计费。
type block struct {
	id    string
	owner string
	size  int64
}

type tenantState struct {
	quota    int64 // -1 不限
	used     int64 // 已结算计费字节
	reserved int64 // 未结算预留字节
}

// Manager 配额管理器。所有方法并发安全；每条状态变更追加一条账本事件。
type Manager struct {
	mu           sync.Mutex
	now          func() time.Time
	ledger       []Event
	tenants      map[string]*tenantState
	reservations map[string]*Reservation
	blocks       map[string]*block
	globalQuota  int64
	globalUsed   int64
	globalResv   int64
}

// NewManager 创建管理器，globalQuota 传 Unlimited 表示全局不限。
func NewManager(globalQuota int64) *Manager {
	return &Manager{
		now:          time.Now,
		tenants:      make(map[string]*tenantState),
		reservations: make(map[string]*Reservation),
		blocks:       make(map[string]*block),
		globalQuota:  globalQuota,
	}
}

func (m *Manager) tenant(name string) *tenantState {
	t, ok := m.tenants[name]
	if !ok {
		t = &tenantState{quota: Unlimited}
		m.tenants[name] = t
	}
	return t
}

func fits(used, reserved, extra, quota int64) bool {
	return quota == Unlimited || used+reserved+extra <= quota
}

func (m *Manager) append(e Event) {
	e.Seq = uint64(len(m.ledger)) + 1
	e.Time = m.now()
	m.ledger = append(m.ledger, e)
}

// Reserve 按预估容量预留。同一 ID 重复调用幂等：参数一致返回原预留单，
// 参数不一致返回 ErrReservationExists。预留占用 租户+全局 两级配额。
func (m *Manager) Reserve(id, tenant string, estimatedBytes int64) (*Reservation, error) {
	if id == "" || tenant == "" || estimatedBytes <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.reservations[id]; ok {
		if r.Tenant != tenant || r.ReservedBytes != estimatedBytes {
			return nil, ErrReservationExists
		}
		cp := *r
		return &cp, nil // 幂等重放
	}

	t := m.tenant(tenant)
	if !fits(t.used, t.reserved, estimatedBytes, t.quota) ||
		!fits(m.globalUsed, m.globalResv, estimatedBytes, m.globalQuota) {
		return nil, ErrQuotaExceeded
	}

	r := &Reservation{ID: id, Tenant: tenant, ReservedBytes: estimatedBytes, State: StateOpen}
	m.reservations[id] = r
	t.reserved += estimatedBytes
	m.globalResv += estimatedBytes
	m.append(Event{Type: EventReserved, Tenant: tenant, ReservationID: id, ReservedBytes: estimatedBytes})
	cp := *r
	return &cp, nil
}

// Settle 压缩完成后按实际大小结算。重复回执幂等：参数一致返回首次结果；
// 压缩后大小或块 ID 不一致返回 ErrSettleConflict。
// 若 blockID 指向已存在的共享物理块，则本次计费为 0（只向归属方计费）。
// 结算时按 实际计费字节 重新校验两级配额（配额可能已调低）；
// 超额则预留单转为 rejected，预留量释放，文件不得发布。
func (m *Manager) Settle(id string, compressedSize int64, blockID string) (*Reservation, error) {
	if id == "" || compressedSize < 0 || blockID == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.reservations[id]
	if !ok {
		return nil, ErrReservationNotFound
	}

	switch r.State {
	case StateSettled:
		if r.CompressedSize != compressedSize || r.BlockID != blockID {
			return nil, ErrSettleConflict
		}
		cp := *r
		return &cp, nil // 重复回执，返回首次结果
	case StateRejected:
		if r.CompressedSize != compressedSize || r.BlockID != blockID {
			return nil, ErrSettleConflict
		}
		return nil, ErrQuotaExceeded // 首次即超额，重放同样拒绝
	case StateCanceled, StateAborted:
		return nil, ErrReservationClosed
	}

	// 命中已有共享块：不计费，归属仍属原租户。
	billed := compressedSize
	if _, exists := m.blocks[blockID]; exists {
		billed = 0
	}

	t := m.tenant(r.Tenant)
	if !fits(t.used, 0, billed, t.quota) || !fits(m.globalUsed, 0, billed, m.globalQuota) {
		// 超额：释放预留，标记拒绝，文件不发布。
		t.reserved -= r.ReservedBytes
		m.globalResv -= r.ReservedBytes
		r.State = StateRejected
		r.CompressedSize = compressedSize
		r.BlockID = blockID
		m.append(Event{
			Type: EventRejected, Tenant: r.Tenant, ReservationID: id,
			ReservedBytes: r.ReservedBytes, ActualBytes: billed, BlockID: blockID,
		})
		return nil, ErrQuotaExceeded
	}

	t.reserved -= r.ReservedBytes
	m.globalResv -= r.ReservedBytes
	t.used += billed
	m.globalUsed += billed
	if billed > 0 {
		m.blocks[blockID] = &block{id: blockID, owner: r.Tenant, size: billed}
	}
	r.State = StateSettled
	r.CompressedSize = compressedSize
	r.BilledBytes = billed
	r.BlockID = blockID
	m.append(Event{
		Type: EventSettled, Tenant: r.Tenant, ReservationID: id,
		ReservedBytes: r.ReservedBytes, ActualBytes: billed, BlockID: blockID,
	})
	cp := *r
	return &cp, nil
}

// Cancel 取消预留并释放配额。对已取消/已中止/已拒绝的预留重复调用幂等成功；
// 对已结算的预留返回 ErrReservationClosed。
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseLocked(id, StateCanceled, EventCanceled)
}

// Abort 压缩失败等场景中止预留并释放配额，幂等语义同 Cancel。
func (m *Manager) Abort(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseLocked(id, StateAborted, EventAborted)
}

func (m *Manager) releaseLocked(id string, state ReservationState, ev EventType) error {
	r, ok := m.reservations[id]
	if !ok {
		return ErrReservationNotFound
	}
	switch r.State {
	case StateOpen:
		t := m.tenant(r.Tenant)
		t.reserved -= r.ReservedBytes
		m.globalResv -= r.ReservedBytes
		r.State = state
		m.append(Event{Type: ev, Tenant: r.Tenant, ReservationID: id, ReservedBytes: r.ReservedBytes})
		return nil
	case StateCanceled, StateAborted, StateRejected:
		return nil // 已释放，幂等成功
	default:
		return ErrReservationClosed // 已结算，不可取消
	}
}

// TransferOwnership 共享物理块归属迁移，原子完成：在同一临界区内
// 校验归属、对迁入方做配额检查、转移计费并落一条账本事件。
// 幂等：块已归属 to 时直接成功。
func (m *Manager) TransferOwnership(blockID, from, to string) error {
	if blockID == "" || from == "" || to == "" || from == to {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blocks[blockID]
	if !ok {
		return ErrBlockNotFound
	}
	if b.owner == to {
		return nil // 幂等
	}
	if b.owner != from {
		return ErrNotOwner
	}

	dst := m.tenant(to)
	if !fits(dst.used, dst.reserved, b.size, dst.quota) {
		return ErrQuotaExceeded // 不迁移，保持原子性：要么全做要么不做
	}

	src := m.tenant(from)
	src.used -= b.size
	dst.used += b.size
	b.owner = to
	m.append(Event{
		Type: EventTransferred, BlockID: blockID,
		FromTenant: from, ToTenant: to, ActualBytes: b.size,
	})
	return nil
}

// SetTenantQuota 调整租户配额，Unlimited 表示不限。允许调低到当前用量之下，
// 后续结算将按新配额校验。
func (m *Manager) SetTenantQuota(tenant string, quota int64) error {
	if tenant == "" || quota < Unlimited {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenant(tenant).quota = quota
	m.append(Event{Type: EventQuotaSet, Tenant: tenant, QuotaBytes: quota})
	return nil
}

// SetGlobalQuota 调整全局配额。
func (m *Manager) SetGlobalQuota(quota int64) error {
	if quota < Unlimited {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.globalQuota = quota
	m.append(Event{Type: EventQuotaSet, QuotaBytes: quota})
	return nil
}

// Usage 租户当前用量：已结算计费字节与未结算预留字节。
func (m *Manager) Usage(tenant string) (used, reserved int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tenant(tenant)
	return t.used, t.reserved
}

// GlobalUsage 全局当前用量。
func (m *Manager) GlobalUsage() (used, reserved int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.globalUsed, m.globalResv
}

// BlockOwner 查询物理块当前归属。
func (m *Manager) BlockOwner(blockID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[blockID]
	if !ok {
		return "", ErrBlockNotFound
	}
	return b.owner, nil
}

// Ledger 返回账本快照（只读副本）。
func (m *Manager) Ledger() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.ledger))
	copy(out, m.ledger)
	return out
}

// Peaks 复算出的峰值用量（已结算 + 在途预留）。
type Peaks struct {
	Global int64
	Tenant map[string]int64
}

// RecomputePeaks 回放事件账本，从零复算全局与各租户的历史峰值用量。
// 复算结束时的终态用量会与当前实时状态一并返回校验。
func RecomputePeaks(ledger []Event) (Peaks, error) {
	used := map[string]int64{}
	reserved := map[string]int64{}
	peaks := Peaks{Tenant: map[string]int64{}}
	var globalUsed, globalReserved, globalPeak int64

	updatePeak := func(tenant string) {
		total := used[tenant] + reserved[tenant]
		if total > peaks.Tenant[tenant] {
			peaks.Tenant[tenant] = total
		}
		if g := globalUsed + globalReserved; g > globalPeak {
			globalPeak = g
		}
	}

	for i, e := range ledger {
		if e.Seq != uint64(i)+1 {
			return Peaks{}, fmt.Errorf("quota: ledger corrupted at seq %d", e.Seq)
		}
		switch e.Type {
		case EventReserved:
			reserved[e.Tenant] += e.ReservedBytes
			globalReserved += e.ReservedBytes
			updatePeak(e.Tenant)
		case EventSettled:
			reserved[e.Tenant] -= e.ReservedBytes
			globalReserved -= e.ReservedBytes
			used[e.Tenant] += e.ActualBytes
			globalUsed += e.ActualBytes
			updatePeak(e.Tenant)
		case EventRejected, EventCanceled, EventAborted:
			reserved[e.Tenant] -= e.ReservedBytes
			globalReserved -= e.ReservedBytes
		case EventTransferred:
			used[e.FromTenant] -= e.ActualBytes
			used[e.ToTenant] += e.ActualBytes
			updatePeak(e.FromTenant)
			updatePeak(e.ToTenant)
		case EventQuotaSet:
			// 配额变更不影响用量峰值
		}
		if reserved[e.Tenant] < 0 || used[e.Tenant] < 0 {
			return Peaks{}, fmt.Errorf("quota: negative usage for tenant %q at seq %d", e.Tenant, e.Seq)
		}
	}
	peaks.Global = globalPeak
	return peaks, nil
}

// VerifyAgainstLedger 回放账本重建终态，并与当前实时状态比对，
// 用于校验运行态与账本的一致性。
func (m *Manager) VerifyAgainstLedger() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	used := map[string]int64{}
	reserved := map[string]int64{}
	var globalUsed, globalReserved int64
	for _, e := range m.ledger {
		switch e.Type {
		case EventReserved:
			reserved[e.Tenant] += e.ReservedBytes
			globalReserved += e.ReservedBytes
		case EventSettled:
			reserved[e.Tenant] -= e.ReservedBytes
			globalReserved -= e.ReservedBytes
			used[e.Tenant] += e.ActualBytes
			globalUsed += e.ActualBytes
		case EventRejected, EventCanceled, EventAborted:
			reserved[e.Tenant] -= e.ReservedBytes
			globalReserved -= e.ReservedBytes
		case EventTransferred:
			used[e.FromTenant] -= e.ActualBytes
			used[e.ToTenant] += e.ActualBytes
		}
	}
	for name, t := range m.tenants {
		if used[name] != t.used || reserved[name] != t.reserved {
			return fmt.Errorf("quota: tenant %q diverged: ledger(%d,%d) live(%d,%d)",
				name, used[name], reserved[name], t.used, t.reserved)
		}
	}
	if globalUsed != m.globalUsed || globalReserved != m.globalResv {
		return fmt.Errorf("quota: global diverged: ledger(%d,%d) live(%d,%d)",
			globalUsed, globalReserved, m.globalUsed, m.globalResv)
	}
	return nil
}
