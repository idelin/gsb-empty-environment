// Package quota 提供磁盘配额预留库：先预约写入容量，压缩完成后按实际大小结算。
//
// 核心语义：
//   - 两级配额：租户级 + 全局级，预留与结算均受约束。
//   - 预留（Reserve）、结算（Settle）、取消（Cancel/Fail）均幂等：
//     每个变更操作携带幂等键，重复回执返回首次结果，不产生重复副作用。
//   - 结算超额时文件不得发布：不记账、不登记物理块、不调用 Publish。
//   - 共享物理块只向归属租户计费；归属迁移（TransferOwnership）原子完成。
//   - 全部变更写入事件账本，可用 ReplayPeak 复算历史峰值占用。
package quota

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrReservationNotFound   = errors.New("quota: reservation not found")
	ErrReservationNotPending = errors.New("quota: reservation not pending")
	ErrBlockNotFound         = errors.New("quota: block not found")
	ErrBlockOwnerMismatch    = errors.New("quota: block owner mismatch")
	ErrTenantLimitExceeded   = errors.New("quota: tenant limit exceeded")
	ErrGlobalLimitExceeded   = errors.New("quota: global limit exceeded")
	ErrInvalidArgument       = errors.New("quota: invalid argument")
)

// ReservationState 预留单状态机。
type ReservationState int

const (
	StatePending   ReservationState = iota // 已预留，待结算
	StateSettled                           // 已按实际大小结算
	StateCancelled                         // 主动取消
	StateFailed                            // 压缩失败，预留释放
	StateRejected                          // 结算超额被拒，预留释放，文件未发布
)

func (s ReservationState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateSettled:
		return "settled"
	case StateCancelled:
		return "cancelled"
	case StateFailed:
		return "failed"
	case StateRejected:
		return "rejected"
	}
	return "unknown"
}

// Reservation 一张预留单。
type Reservation struct {
	ID       string
	Tenant   string
	Bytes    int64 // 预留容量（压缩前估值）
	State    ReservationState
	Billed   int64 // 结算后实际计费字节数
	CreateAt time.Time
}

// Block 一个物理块。多个文件可共享同一物理块（去重），
// 但只有归属租户为其计费。
type Block struct {
	ID   string
	Size int64
}

// opResult 幂等结果缓存：同一幂等键重复提交时原样返回。
type opResult struct {
	res *Reservation
	err error
}

// Usage 某时刻占用快照。
type Usage struct {
	Reserved int64 // 待结算的预留占用
	Used     int64 // 已结算计费占用
}

// Total 返回总占用（预留 + 已用）。
func (u Usage) Total() int64 { return u.Reserved + u.Used }

// Store 配额存储。所有方法并发安全；单互斥锁保证
// 归属迁移与配额检查-记账的原子性。
type Store struct {
	mu sync.Mutex

	globalLimit int64
	tenantLimit map[string]int64

	reserved map[string]int64 // tenant -> 待结算预留字节
	used     map[string]int64 // tenant -> 已结算计费字节

	reservations map[string]*Reservation
	blocks       map[string]blockMeta // blockID -> meta
	idem         map[string]*opResult // idempotency key -> 首次结果

	ledger []Event
	seq    int64
	now    func() time.Time // 可注入时钟，便于测试
}

type blockMeta struct {
	owner string
	size  int64
}

// NewStore 创建配额存储。globalLimit 为全局总配额（字节）。
func NewStore(globalLimit int64) *Store {
	return &Store{
		globalLimit:  globalLimit,
		tenantLimit:  make(map[string]int64),
		reserved:     make(map[string]int64),
		used:         make(map[string]int64),
		reservations: make(map[string]*Reservation),
		blocks:       make(map[string]blockMeta),
		idem:         make(map[string]*opResult),
		now:          time.Now,
	}
}

// SetTenantLimit 设置/调低租户限额。允许调到低于当前占用：
// 存量不强制回收，但此后该租户任何新增预留/结算都会被拒绝。
func (s *Store) SetTenantLimit(tenant string, limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenantLimit[tenant] = limit
	s.appendLocked(Event{
		Type:   EventLimitChange,
		Tenant: tenant,
		Limit:  limit,
	})
}

// SetGlobalLimit 调整全局限额，语义同 SetTenantLimit。
func (s *Store) SetGlobalLimit(limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalLimit = limit
	s.appendLocked(Event{Type: EventLimitChange, Limit: limit})
}

// Reserve 预约写入容量。key 为幂等键：重复提交返回同一张预留单。
func (s *Store) Reserve(key, tenant string, bytes int64) (Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.idem["reserve/"+key]; ok {
		return *r.res, r.err
	}
	res, err := s.reserveLocked(tenant, bytes)
	s.idem["reserve/"+key] = &opResult{res: res, err: err}
	return *res, err
}

func (s *Store) reserveLocked(tenant string, bytes int64) (*Reservation, error) {
	if tenant == "" || bytes <= 0 {
		return &Reservation{}, ErrInvalidArgument
	}
	if err := s.checkLimitLocked(tenant, bytes); err != nil {
		return &Reservation{}, err
	}
	s.seq++
	res := &Reservation{
		ID:       fmt.Sprintf("rsv-%d", s.seq),
		Tenant:   tenant,
		Bytes:    bytes,
		State:    StatePending,
		CreateAt: s.now(),
	}
	s.reservations[res.ID] = res
	s.reserved[tenant] += bytes
	s.appendLocked(Event{
		Type:   EventReserve,
		Tenant: tenant,
		ResID:  res.ID,
		Bytes:  bytes,
	})
	return res, nil
}

// checkLimitLocked 校验租户与全局两级配额是否容纳 delta 新增占用。
func (s *Store) checkLimitLocked(tenant string, delta int64) error {
	if limit, ok := s.tenantLimit[tenant]; ok {
		if s.reserved[tenant]+s.used[tenant]+delta > limit {
			return ErrTenantLimitExceeded
		}
	}
	if s.totalLocked()+delta > s.globalLimit {
		return ErrGlobalLimitExceeded
	}
	return nil
}

func (s *Store) totalLocked() int64 {
	var total int64
	for t := range s.reserved {
		total += s.reserved[t] + s.used[t]
	}
	return total
}

// Settle 按压缩后的实际物理块结算。
//
// 计费规则：blocks 中已存在于存储的块（他租户的共享块或本租户已有块）
// 不重复计费；仅新块计入本租户 used 并登记归属。
// 若计费后超出配额：预留释放、不记账、不登记块、不调用 publish，
// 返回配额错误——文件不得发布。
// publish 在记账成功后调用；其失败不回滚已发生的物理写入。
//
// 幂等：同一 key 的重复回执返回首次结果，不重复计费、不重复发布。
func (s *Store) Settle(key, resID string, blocks []Block, publish func() error) error {
	s.mu.Lock()
	ikey := "settle/" + key
	if r, ok := s.idem[ikey]; ok {
		s.mu.Unlock()
		return r.err
	}
	err := s.settleLocked(resID, blocks)
	if err == nil && publish != nil {
		// 记账已提交；发布失败不回滚配额（物理字节已落盘）。
		if perr := publish(); perr != nil {
			err = perr
		}
	}
	s.idem[ikey] = &opResult{err: err}
	s.mu.Unlock()
	return err
}

func (s *Store) settleLocked(resID string, blocks []Block) error {
	res, ok := s.reservations[resID]
	if !ok {
		return ErrReservationNotFound
	}
	if res.State != StatePending {
		return ErrReservationNotPending
	}
	tenant := res.Tenant

	// 先释放预留，再按实际计费，保证峰值语义正确。
	var billed int64
	var newBlocks []Block
	for _, b := range blocks {
		if b.Size <= 0 {
			return ErrInvalidArgument
		}
		if _, exists := s.blocks[b.ID]; exists {
			continue // 共享块：只向归属方计费，本租户不计
		}
		billed += b.Size
		newBlocks = append(newBlocks, b)
	}

	if err := s.checkLimitLocked(tenant, billed-res.Bytes); err != nil {
		// 结算超额：释放预留，拒绝发布。
		s.reserved[tenant] -= res.Bytes
		res.State = StateRejected
		s.appendLocked(Event{
			Type:   EventSettleRejected,
			Tenant: tenant,
			ResID:  resID,
			Bytes:  res.Bytes,
			Billed: billed,
		})
		return err
	}

	s.reserved[tenant] -= res.Bytes
	s.used[tenant] += billed
	for _, b := range newBlocks {
		s.blocks[b.ID] = blockMeta{owner: tenant, size: b.Size}
	}
	res.State = StateSettled
	res.Billed = billed
	s.appendLocked(Event{
		Type:   EventSettle,
		Tenant: tenant,
		ResID:  resID,
		Bytes:  res.Bytes,
		Billed: billed,
	})
	return nil
}

// Cancel 主动取消预留并释放容量。幂等：对已取消/已释放的预留单
// 重复取消视为成功空操作；同一幂等键重放返回首次结果。
func (s *Store) Cancel(key, resID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ikey := "cancel/" + key
	if r, ok := s.idem[ikey]; ok {
		return r.err
	}
	err := s.releaseLocked(resID, StateCancelled, EventCancel, "")
	s.idem[ikey] = &opResult{err: err}
	return err
}

// Fail 标记压缩失败并释放预留。语义与 Cancel 相同，状态与事件可区分。
func (s *Store) Fail(key, resID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ikey := "fail/" + key
	if r, ok := s.idem[ikey]; ok {
		return r.err
	}
	err := s.releaseLocked(resID, StateFailed, EventFail, reason)
	s.idem[ikey] = &opResult{err: err}
	return err
}

func (s *Store) releaseLocked(resID string, state ReservationState, et EventType, reason string) error {
	res, ok := s.reservations[resID]
	if !ok {
		return ErrReservationNotFound
	}
	switch res.State {
	case StatePending:
		s.reserved[res.Tenant] -= res.Bytes
		res.State = state
		s.appendLocked(Event{
			Type:   et,
			Tenant: res.Tenant,
			ResID:  resID,
			Bytes:  res.Bytes,
			Reason: reason,
		})
		return nil
	case StateCancelled, StateFailed, StateRejected:
		return nil // 已释放：幂等空操作
	default:
		return ErrReservationNotPending // 已结算不可取消
	}
}

// Usage 返回租户当前占用快照。
func (s *Store) Usage(tenant string) Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Usage{Reserved: s.reserved[tenant], Used: s.used[tenant]}
}

// GlobalUsage 返回全局当前占用快照。
func (s *Store) GlobalUsage() Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var u Usage
	for t := range s.reserved {
		u.Reserved += s.reserved[t]
		u.Used += s.used[t]
	}
	return u
}

// Reservation 查询预留单快照。
func (s *Store) Reservation(resID string) (Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.reservations[resID]
	if !ok {
		return Reservation{}, ErrReservationNotFound
	}
	return *res, nil
}

// BlockOwner 查询物理块归属租户。
func (s *Store) BlockOwner(blockID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.blocks[blockID]
	if !ok {
		return "", ErrBlockNotFound
	}
	return m.owner, nil
}
