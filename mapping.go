package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
)

// pageSize 是镜像映射的粒度（字节）。
const pageSize = 4096

// maxAddress 是 GPU 共享虚拟地址空间的上界（不含）。
const maxAddress = uint64(1) << 48

var (
	// ErrBadPASID 表示 PASID 未知、已回收或代次过期。
	ErrBadPASID = errors.New("netsec: unknown or stale PASID")
	// ErrNotOwner 表示命令引用了属于其他进程的 PASID。
	ErrNotOwner = errors.New("netsec: PASID not owned by submitting process")
	// ErrUnmapped 表示虚拟地址当前没有镜像映射。
	ErrUnmapped = errors.New("netsec: virtual address not mapped")
	// ErrNoPASID 表示 PASID 池耗尽。
	ErrNoPASID = errors.New("netsec: no PASID available")
)

// PASID 标识一个 GPU 地址空间。代次（Gen）在每次回收复用时递增，
// 使回收前残留的 TLB 条目因代次不匹配而失效。
type PASID struct {
	ID  uint32
	Gen uint64
}

// Invalidator 在 TLB 失效路径上被调用。它在返回之前必须保证
// GPU 侧 [start, end) 范围内、属于该 PASID（含代次）的 TLB 条目
// 已全部失效。测试中可注入阻塞实现以模拟并发失效。
type Invalidator func(p PASID, start, end uint64)

// Page 是被镜像进 GPU 地址空间的物理页。回收（free）仅发生在
// 引用计数归零时，而映射持有的引用只会在 TLB 失效完成后释放。
type Page struct {
	id   uint64
	refs atomic.Int32
	free atomic.Bool
}

// NewPage 返回一个调用方持有一个引用的页。
func NewPage(id uint64) *Page {
	p := &Page{id: id}
	p.refs.Store(1)
	return p
}

// ID 返回页的标识。
func (p *Page) ID() uint64 { return p.id }

// Freed 报告页是否已被回收。
func (p *Page) Freed() bool { return p.free.Load() }

func (p *Page) acquire() { p.refs.Add(1) }

func (p *Page) release() {
	if p.refs.Add(-1) == 0 {
		p.free.Store(true)
	}
}

// Process 是 PASID 属主的 opaque 令牌，用于校验命令提交路径。
type Process struct{ id uint64 }

var procSeq atomic.Uint64

// NewProcess 创建一个新的进程令牌。
func NewProcess() *Process { return &Process{id: procSeq.Add(1)} }

// Command 是 GPU 提交路径上的命令，携带目标地址空间与访问的虚拟地址。
type Command struct {
	PASID PASID
	Addrs []uint64
}

type pasidState struct {
	gen   uint64
	owner *Process
	inUse bool
}

// Manager 管理 PASID 生命周期与 CPU 页表的 GPU 镜像映射，
// 供内存管理通知（munmap 等）与 GPU 提交路径并发调用。
type Manager struct {
	inv Invalidator

	mu     sync.RWMutex
	seq    atomic.Uint64 // 映射变更序号，取页路径据此重试
	spaces map[PASID]map[uint64]*Page
	states map[uint32]*pasidState
	free   []uint32 // 已完成 TLB 失效、可复用的 ID
	next   uint32
	maxID  uint32

	retries atomic.Uint64

	// afterLookupHook 是测试注入点：在 TakePage 查表之后、
	// 序号复核之前调用，用于确定性制造序号变化。
	afterLookupHook func()
}

// NewManager 创建一个映射管理器。inv 为 nil 时使用空失效器。
// maxPASID 限定同时存在的 PASID ID 数量，ID 用尽后只能复用已回收的。
func NewManager(inv Invalidator, maxPASID uint32) *Manager {
	if inv == nil {
		inv = func(PASID, uint64, uint64) {}
	}
	return &Manager{
		inv:    inv,
		spaces: make(map[PASID]map[uint64]*Page),
		states: make(map[uint32]*pasidState),
		maxID:  maxPASID,
	}
}

// Retries 返回 TakePage 因序号变化而丢弃重来的累计次数。
func (m *Manager) Retries() uint64 { return m.retries.Load() }

func (m *Manager) validLocked(p PASID) bool {
	st, ok := m.states[p.ID]
	return ok && st.inUse && st.gen == p.Gen
}

// AllocPASID 为 owner 分配一个 PASID。复用回收 ID 时递增代次，
// 使旧代次的残留 TLB 条目与命令全部失效。回收的 ID 只有在
// ReleasePASID 完成 TLB 失效之后才会进入可复用池。
func (m *Manager) AllocPASID(owner *Process) (PASID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.free); n > 0 {
		id := m.free[n-1]
		m.free = m.free[:n-1]
		st := m.states[id]
		st.gen++
		st.inUse = true
		st.owner = owner
		p := PASID{ID: id, Gen: st.gen}
		m.spaces[p] = make(map[uint64]*Page)
		m.seq.Add(1)
		return p, nil
	}
	if m.next >= m.maxID {
		return PASID{}, ErrNoPASID
	}
	id := m.next
	m.next++
	m.states[id] = &pasidState{gen: 1, owner: owner, inUse: true}
	p := PASID{ID: id, Gen: 1}
	m.spaces[p] = make(map[uint64]*Page)
	m.seq.Add(1)
	return p, nil
}

// ReleasePASID 回收 PASID。先在锁内注销使新取页失败并推进序号，
// 然后对整段地址空间做 TLB 失效；失效返回之前，旧页不得回收，
// 该 ID 也不会进入可复用池。
func (m *Manager) ReleasePASID(owner *Process, p PASID) error {
	m.mu.Lock()
	st, ok := m.states[p.ID]
	if !ok || !st.inUse || st.gen != p.Gen {
		m.mu.Unlock()
		return ErrBadPASID
	}
	if st.owner != owner {
		m.mu.Unlock()
		return ErrNotOwner
	}
	st.inUse = false
	st.owner = nil
	pages := m.spaces[p]
	delete(m.spaces, p)
	m.seq.Add(1)
	m.mu.Unlock()

	m.inv(p, 0, maxAddress)
	for _, pg := range pages {
		pg.release()
	}

	m.mu.Lock()
	m.free = append(m.free, p.ID)
	m.mu.Unlock()
	return nil
}

// Map 将 va 镜像映射到 pg。若 va 已有映射，旧映射先被摘除并推进序号，
// 待 TLB 失效返回后才释放旧页的映射引用。
func (m *Manager) Map(p PASID, va uint64, pg *Page) error {
	m.mu.Lock()
	if !m.validLocked(p) {
		m.mu.Unlock()
		return ErrBadPASID
	}
	space := m.spaces[p]
	old := space[va]
	pg.acquire()
	space[va] = pg
	m.seq.Add(1)
	m.mu.Unlock()

	if old != nil {
		m.inv(p, va, va+pageSize)
		old.release()
	}
	return nil
}

// UnmapNotify 是内存管理通知入口（如 munmap）：摘除 [start, end)
// 内的镜像映射并推进序号，随后执行 TLB 失效。失效通知返回之前，
// 旧页的映射引用不会被释放，因此旧页不会被回收。
func (m *Manager) UnmapNotify(p PASID, start, end uint64) error {
	m.mu.Lock()
	if !m.validLocked(p) {
		m.mu.Unlock()
		return ErrBadPASID
	}
	space := m.spaces[p]
	var victims []*Page
	for va, pg := range space {
		if va >= start && va < end {
			victims = append(victims, pg)
			delete(space, va)
		}
	}
	m.seq.Add(1)
	m.mu.Unlock()

	m.inv(p, start, end)
	for _, pg := range victims {
		pg.release()
	}
	return nil
}

// TakePage 供 GPU 提交路径按序号重试地取页：查表前记下序号，
// 加锁查表并加引用后复核序号，序号变化就丢弃引用重来。
// 成功返回的页持有一个引用，调用方用完必须 PutPage。
func (m *Manager) TakePage(p PASID, va uint64) (*Page, error) {
	for {
		seq := m.seq.Load()
		m.mu.RLock()
		valid := m.validLocked(p)
		var pg *Page
		if valid {
			pg = m.spaces[p][va]
			if pg != nil {
				pg.acquire()
			}
		}
		m.mu.RUnlock()
		if m.afterLookupHook != nil {
			m.afterLookupHook()
		}
		if !valid {
			return nil, ErrBadPASID
		}
		if pg == nil {
			return nil, ErrUnmapped
		}
		if m.seq.Load() != seq {
			m.retries.Add(1)
			pg.release()
			continue
		}
		return pg, nil
	}
}

// PutPage 释放 TakePage 取得的引用。
func (m *Manager) PutPage(pg *Page) { pg.release() }

func (m *Manager) checkOwner(owner *Process, p PASID) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[p.ID]
	if !ok || !st.inUse || st.gen != p.Gen {
		return ErrBadPASID
	}
	if st.owner != owner {
		return ErrNotOwner
	}
	return nil
}

// Submit 校验命令的 PASID 归属与代次，随后为命令访问的每个
// 虚拟地址取页、执行、放回。引用他人 PASID 或代次过期的命令
// 会被拒绝。
func (m *Manager) Submit(owner *Process, cmd Command) error {
	if err := m.checkOwner(owner, cmd.PASID); err != nil {
		return err
	}
	pages := make([]*Page, 0, len(cmd.Addrs))
	for _, va := range cmd.Addrs {
		pg, err := m.TakePage(cmd.PASID, va)
		if err != nil {
			for _, held := range pages {
				held.release()
			}
			return err
		}
		pages = append(pages, pg)
	}
	for _, pg := range pages {
		pg.release()
	}
	return nil
}
