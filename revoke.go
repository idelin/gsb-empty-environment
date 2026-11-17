package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 映射生命周期状态。
const (
	stateActive   = iota // 可正常读取，尚未进入撤权事务
	stateRevoking        // 撤权屏障已拦截，等待失效确认
	stateIsolated        // 失效确认失败，永久隔离、编号不再回收
)

// 撤权事务阶段，描述一次 Revoke 的三阶段屏障协议。
const (
	PhaseIdle       = "idle"       // 没有进行中的撤权事务
	PhaseBarrier    = "barrier"    // 第一阶段：屏障已升起，新映射被阻止
	PhaseDraining   = "draining"   // 第二阶段：等待所有已登记映射完成失效确认
	PhaseFinalizing = "finalizing" // 第三阶段：回收成功编号、隔离失败编号
)

var (
	// ErrBarrier 表示撤权屏障已升起，事务期间不接受新映射。
	ErrBarrier = errors.New("netsec: revocation barrier active, new mappings are blocked")
	// ErrInvalidMapping 表示引用未登记或已被回收。
	ErrInvalidMapping = errors.New("netsec: mapping is not registered")
	// ErrStaleMapping 表示引用属于编号重用前的旧代际，其确认必须被拒绝。
	ErrStaleMapping = errors.New("netsec: stale mapping generation after id reuse")
	// ErrIsolatedMapping 表示映射因失效确认失败而处于隔离状态，不可复用。
	ErrIsolatedMapping = errors.New("netsec: mapping is isolated and cannot be reused")
	// ErrAlreadyInvalidated 表示失效确认已提交过，重复确认被拒绝。
	ErrAlreadyInvalidated = errors.New("netsec: mapping already invalidated")
	// ErrInvalidationFailed 表示撤权事务中存在未成功完成失效确认的映射。
	ErrInvalidationFailed = errors.New("netsec: invalidation failure during revocation")
)

// Page 是一块模拟共享页。隔离运行器中的映射只能读取它的副本，
// 撤权成功意味着宿主侧不再保留任何可读取该页的有效映射。
type Page struct {
	data []byte
}

// NewPage 创建一块带有初始内容的模拟共享页。
func NewPage(data []byte) *Page {
	copied := make([]byte, len(data))
	copy(copied, data)
	return &Page{data: copied}
}

// Size 返回模拟共享页的字节数。
func (p *Page) Size() int {
	return len(p.data)
}

// MappingRef 标识一次租户到模拟共享页的映射登记。
// 同一编号在回收重用后会产生新的代际，旧引用的确认必须被识别并拒绝。
type MappingRef struct {
	id         uint64
	generation uint64
	tenant     string
}

// ID 返回映射编号。
func (m MappingRef) ID() uint64 { return m.id }

// Generation 返回映射代际，每次编号重用都会递增。
func (m MappingRef) Generation() uint64 { return m.generation }

// Tenant 返回申请该映射的租户标识。
func (m MappingRef) Tenant() string { return m.tenant }

func (m MappingRef) String() string {
	return fmt.Sprintf("MappingRef(id=%d, generation=%d, tenant=%q)", m.id, m.generation, m.tenant)
}

type mappingSlot struct {
	ref       MappingRef
	page      *Page
	state     int
	confirmed bool
	recycled  bool
	err       error
}

// revokeTxn 是一次撤权事务的状态。所有等待者共享同一事务，
// 收尾仅在 pending 归零后通过 once 触发，并发触发下保证只执行一次。
type revokeTxn struct {
	doneCh       chan struct{}
	once         sync.Once
	err          error
	pending      int
	initialCount int
}

// Host 是 Go 控制宿主：登记租户映射，并通过三阶段屏障事务撤销共享页访问。
type Host struct {
	mu          sync.Mutex
	slots       map[uint64]*mappingSlot
	generations map[uint64]uint64
	nextID      uint64
	freeIDs     []uint64
	quarantined map[uint64]struct{}
	revoking    bool
	txn         *revokeTxn
	lastTxnErr  error
}

// NewHost 创建一个没有任何登记映射的控制宿主。
func NewHost() *Host {
	return &Host{
		slots:       make(map[uint64]*mappingSlot),
		generations: make(map[uint64]uint64),
		nextID:      1,
		quarantined: make(map[uint64]struct{}),
	}
}

// RevocationPhase 返回当前撤权事务阶段，主要用于可观测性与测试。
func (h *Host) RevocationPhase() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.phaseLocked()
}

func (h *Host) phaseLocked() string {
	if h.txn == nil {
		return PhaseIdle
	}
	if !h.revoking {
		return PhaseFinalizing
	}
	if h.allDrainedLocked() {
		return PhaseFinalizing
	}
	if h.txn.pending == h.txn.initialCount {
		return PhaseBarrier
	}
	return PhaseDraining
}

func (h *Host) allDrainedLocked() bool {
	for _, slot := range h.slots {
		if slot.state == stateRevoking && !slot.confirmed {
			return false
		}
	}
	return true
}

// Map 为租户登记一块模拟共享页的映射。
// 撤权事务进行中调用会立即收到 ErrBarrier，新租户申请必须等待事务结束。
func (h *Host) Map(tenant string, page *Page) (MappingRef, error) {
	if tenant == "" {
		return MappingRef{}, errors.New("netsec: tenant must not be empty")
	}
	if page == nil {
		return MappingRef{}, errors.New("netsec: page must not be nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.revoking || h.txn != nil {
		return MappingRef{}, ErrBarrier
	}

	var id uint64
	if len(h.freeIDs) > 0 {
		id = h.freeIDs[len(h.freeIDs)-1]
		h.freeIDs = h.freeIDs[:len(h.freeIDs)-1]
	} else {
		id = h.nextID
		h.nextID++
	}

	generation := uint64(1)
	if latest, ok := h.generations[id]; ok {
		generation = latest + 1
	}
	h.generations[id] = generation
	ref := MappingRef{id: id, generation: generation, tenant: tenant}
	h.slots[id] = &mappingSlot{
		ref:   ref,
		page:  page,
		state: stateActive,
	}
	return ref, nil
}

// Read 返回映射页内容的副本。撤权屏障升起后读取立即失败，
// 确保策略变更不会留下仍可读取的映射。
func (h *Host) Read(ref MappingRef) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	slot, err := h.resolveLocked(ref)
	if err != nil {
		return nil, err
	}
	if slot.state == stateRevoking {
		return nil, fmt.Errorf("%w: mapping %d revoked before invalidation", ErrBarrier, ref.id)
	}
	out := make([]byte, len(slot.page.data))
	copy(out, slot.page.data)
	return out, nil
}

// ConfirmInvalidation 提交某条映射的失效确认。
// success 为 false 表示底层解除映射失败，该映射进入永久隔离状态，
// 事务不会因此回滚，但编号永不回收、不可复用。
func (h *Host) ConfirmInvalidation(ref MappingRef, success bool, cause error) error {
	h.mu.Lock()
	slot, ok := h.slots[ref.id]
	if !ok || slot.ref.generation != ref.generation {
		h.mu.Unlock()
		if _, isolated := h.quarantined[ref.id]; isolated {
			return fmt.Errorf("%w: mapping %d", ErrIsolatedMapping, ref.id)
		}
		if ok {
			return fmt.Errorf("%w: mapping %d", ErrStaleMapping, ref.id)
		}
		return fmt.Errorf("%w: mapping %d", ErrInvalidMapping, ref.id)
	}
	if slot.state == stateIsolated {
		h.mu.Unlock()
		return fmt.Errorf("%w: mapping %d", ErrIsolatedMapping, ref.id)
	}
	if slot.confirmed {
		h.mu.Unlock()
		return fmt.Errorf("%w: mapping %d", ErrAlreadyInvalidated, ref.id)
	}
	if slot.state != stateRevoking {
		h.mu.Unlock()
		return fmt.Errorf("%w: mapping %d is not under revocation", ErrInvalidMapping, ref.id)
	}
	slot.confirmed = true
	if success {
		// 成功确认保持 stateRevoking，收尾时回收编号。
	} else {
		slot.state = stateIsolated
		if cause == nil {
			cause = errors.New("unspecified invalidation failure")
		}
		slot.err = cause
	}
	txn := h.txn
	txn.pending--
	shouldFinalize := txn.pending == 0
	h.mu.Unlock()

	if shouldFinalize {
		txn.once.Do(func() { h.finalize(txn) })
	}
	return nil
}

// resolveLocked 校验引用的登记状态与代际，失败映射同样拒绝复用。
func (h *Host) resolveLocked(ref MappingRef) (*mappingSlot, error) {
	slot, ok := h.slots[ref.id]
	if !ok {
		if _, isolated := h.quarantined[ref.id]; isolated {
			return nil, fmt.Errorf("%w: mapping %d", ErrIsolatedMapping, ref.id)
		}
		return nil, fmt.Errorf("%w: mapping %d", ErrInvalidMapping, ref.id)
	}
	if slot.ref.generation != ref.generation {
		return nil, fmt.Errorf("%w: mapping %d", ErrStaleMapping, ref.id)
	}
	if slot.state == stateIsolated {
		return nil, fmt.Errorf("%w: mapping %d", ErrIsolatedMapping, ref.id)
	}
	if slot.recycled {
		return nil, fmt.Errorf("%w: mapping %d", ErrInvalidMapping, ref.id)
	}
	return slot, nil
}

// BeginRevocation 同步开启撤权事务并立即返回：
//  1. 升起屏障，阻止一切新映射申请；
//  2. 将所有已登记映射标记为失效中，其读取即刻失败，等待失效确认。
//
// 若已有进行中的事务，BeginRevocation 直接返回该事务，屏障保持升起。
// 调用方随后通过 Revoke 等待全部失效确认并完成收尾。
func (h *Host) BeginRevocation() {
	h.mu.Lock()
	if h.txn == nil {
		pending := 0
		for _, slot := range h.slots {
			if slot.state != stateIsolated {
				pending++
			}
		}
		h.txn = &revokeTxn{doneCh: make(chan struct{}), pending: pending, initialCount: pending}
		h.revoking = true
		for _, slot := range h.slots {
			if slot.state != stateIsolated {
				slot.state = stateRevoking
			}
		}
	}
	h.mu.Unlock()
}

// Revoke 等待当前撤权事务完成：所有已登记映射必须先完成失效确认，
// 随后回收确认成功的映射编号，隔离确认失败的映射并返回事务错误。
//
// 若尚无进行中的事务（包括上一事务已经收尾），Revoke 直接返回上一事务的
// 结果（没有历史事务时为 nil），不会凭空开启新事务；
// 需要开启事务请显式调用 BeginRevocation。
// 多个 goroutine 可并发调用，它们共享同一事务并在同一屏障上等待；
// 即使某个调用者的 ctx 被取消，事务仍在后台推进，后续调用可以继续等待结果。
func (h *Host) Revoke(ctx context.Context) error {
	h.mu.Lock()
	txn := h.txn
	if txn == nil {
		lastErr := h.lastTxnErr
		h.mu.Unlock()
		return lastErr
	}
	shouldFinalize := txn.pending == 0
	h.mu.Unlock()

	if shouldFinalize {
		txn.once.Do(func() { h.finalize(txn) })
	}

	select {
	case <-txn.doneCh:
		return txn.err
	case <-ctx.Done():
		// 收尾是幂等的：若确认恰好已全部到达，即使本调用者被取消也应完成事务。
		h.mu.Lock()
		shouldFinalize = txn.pending == 0
		h.mu.Unlock()
		if shouldFinalize {
			txn.once.Do(func() { h.finalize(txn) })
		}
		return ctx.Err()
	}
}

// finalize 是撤权事务的收尾：回收成功映射、隔离失败映射、降下屏障。
func (h *Host) finalize(txn *revokeTxn) {
	h.mu.Lock()
	var failures []error
	for id, slot := range h.slots {
		if _, alreadyQuarantined := h.quarantined[id]; alreadyQuarantined {
			continue
		}
		if !slot.confirmed {
			// 收尾仅在全部确认到达后触发，这里属于保护性检查。
			continue
		}
		if slot.state == stateIsolated {
			h.quarantined[id] = struct{}{}
			failures = append(failures, fmt.Errorf("%w: mapping %d (tenant %q): %v",
				ErrInvalidationFailed, id, slot.ref.tenant, slot.err))
			continue
		}
		slot.recycled = true
		slot.page = nil
		h.freeIDs = append(h.freeIDs, id)
	}
	sort.Slice(h.freeIDs, func(i, j int) bool { return h.freeIDs[i] < h.freeIDs[j] })
	if len(failures) > 0 {
		txn.err = errors.Join(failures...)
	}
	h.lastTxnErr = txn.err
	h.revoking = false
	h.txn = nil
	close(txn.doneCh)
	h.mu.Unlock()
}

// IsolatedIDs 返回因失效确认失败而被永久隔离的映射编号快照。
func (h *Host) IsolatedIDs() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]uint64, 0, len(h.quarantined))
	for id := range h.quarantined {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// ActiveMappings 返回当前仍登记在册（含失效中）的映射引用快照。
func (h *Host) ActiveMappings() []MappingRef {
	h.mu.Lock()
	defer h.mu.Unlock()
	refs := make([]MappingRef, 0, len(h.slots))
	for _, slot := range h.slots {
		refs = append(refs, slot.ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].id != refs[j].id {
			return refs[i].id < refs[j].id
		}
		return refs[i].generation < refs[j].generation
	})
	return refs
}

// inRevocation 报告撤权屏障是否处于升起状态，供包内测试同步事务边界。
func (h *Host) inRevocation() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revoking || h.txn != nil
}

// pendingInvalidations 返回当前事务中尚未完成失效确认的映射数量，供包内测试断言排空进度。
func (h *Host) pendingInvalidations() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.txn == nil {
		return 0
	}
	return h.txn.pending
}
