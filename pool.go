// Package pool 实现一个带代号（代数）句柄的定长对象池。
//
// 池向调用者返回 Handle{槽号, 代号}。槽被回收复用时代号递增，
// 使此前发出的旧句柄全部失效。借用期间删除只把对象标记为待回收，
// 槽要等最后一个引用被释放后才会复用。当代号到达上限、再回收
// 就会溢出时，该槽被永久退休，代号绝不绕回。
package pool

// Handle 是池发给的句柄，仅对签发它的 Pool 有意义。
type Handle struct {
	Slot int // 槽号
	Gen  int // 代号
}

type slot struct {
	gen     int  // 当前代号，单调递增，永不绕回
	refs    int  // 未释放的引用数
	live    bool // 已创建（可能正被借用）
	deleted bool // 已删除，等待最后一次释放后回收
	retired bool // 代号溢出，永久退役
}

// Pool 是定长对象池，对象由 (槽号, 代号) 句柄寻址。
type Pool struct {
	maxGen int
	slots  []slot
}

// New 返回一个含 size 个槽的池，代号空间为 0..maxGen。
// 每个槽最多经历 maxGen+1 代，之后永久退休。
func New(size, maxGen int) *Pool {
	if size <= 0 {
		panic("pool: size must be positive")
	}
	if maxGen < 0 {
		panic("pool: maxGen must be non-negative")
	}
	return &Pool{maxGen: maxGen, slots: make([]slot, size)}
}

// Create 分配一个对象并返回句柄，调用者持有一个引用。
// 所有槽都在使用中或已退休时 ok 为 false。
func (p *Pool) Create() (h Handle, ok bool) {
	for i := range p.slots {
		s := &p.slots[i]
		if s.live || s.retired {
			continue
		}
		s.live = true
		s.refs = 1
		return Handle{Slot: i, Gen: s.gen}, true
	}
	return Handle{}, false
}

// Borrow 为 h 所指对象增加一个引用并返回可用句柄。
// h 已失效，或对象已标记待回收时失败。
func (p *Pool) Borrow(h Handle) (Handle, bool) {
	s := p.slot(h)
	if s == nil || s.deleted {
		return Handle{}, false
	}
	s.refs++
	return h, true
}

// Delete 把 h 所指对象标记为待回收。槽要等最后一个引用
// 被释放后才复用；若当前无人借用则立即回收。
// 句柄失效或重复删除时返回 false。
func (p *Pool) Delete(h Handle) bool {
	s := p.slot(h)
	if s == nil || s.deleted {
		return false
	}
	s.deleted = true
	if s.refs == 0 {
		p.reclaim(h.Slot)
	}
	return true
}

// Release 释放 h 所指对象的一个引用。待回收对象的最后一个
// 引用被释放时，槽被回收复用。
func (p *Pool) Release(h Handle) bool {
	s := p.slot(h)
	if s == nil || s.refs == 0 {
		return false
	}
	s.refs--
	if s.refs == 0 && s.deleted {
		p.reclaim(h.Slot)
	}
	return true
}

// Valid 报告 h 当前是否指向一个存活对象。待回收对象的句柄
// 在最后一次释放前仍然有效。
func (p *Pool) Valid(h Handle) bool {
	return p.slot(h) != nil
}

// slot 返回 h 对应的槽；h 越界或已失效时返回 nil。
func (p *Pool) slot(h Handle) *slot {
	if h.Slot < 0 || h.Slot >= len(p.slots) {
		return nil
	}
	s := &p.slots[h.Slot]
	if !s.live || s.gen != h.Gen {
		return nil
	}
	return s
}

// reclaim 回收第 i 个槽并递增代号，使所有旧句柄失效。
// 代号若再增就会溢出，则永久退休该槽，绝不绕回。
func (p *Pool) reclaim(i int) {
	s := &p.slots[i]
	s.live = false
	s.deleted = false
	s.refs = 0
	if s.gen == p.maxGen {
		s.retired = true
		return
	}
	s.gen++
}
