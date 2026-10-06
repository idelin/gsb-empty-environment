// Package slotpool 实现固定容量的对象池：向外发放由槽号与代号组成的句柄。
//
// 语义约定：
//   - 句柄 = {槽号, 代号}，代号从 1 开始，0 表示无效句柄。
//   - 槽被回收复用后代号递增，旧句柄随之永久失效。
//   - 借用期间调用 Delete 只把槽标记为待回收，槽仍处借用状态；
//     直到最后一次 Release 才真正回收并允许复用。
//   - 代号空间有限（genBits 位），代号递增至溢出时该槽永久退休，绝不绕回。
package slotpool

import "errors"

var (
	// ErrPoolExhausted 表示没有可分配的槽（全部借用中或已退休）。
	ErrPoolExhausted = errors.New("slotpool: pool exhausted")
	// ErrInvalidHandle 表示句柄无效：代号不匹配、槽未借出或已退休。
	ErrInvalidHandle = errors.New("slotpool: invalid handle")
)

// Handle 是池发放给调用者的句柄，零值无效。
type Handle struct {
	Slot uint32
	Gen  uint32
}

type slotState uint8

const (
	stateFree slotState = iota
	stateBorrowed
	stateRetired
)

type slot struct {
	gen           uint32
	state         slotState
	pendingDelete bool
}

// Pool 是固定容量的槽位池，非并发安全。
type Pool struct {
	slots  []slot
	free   []uint32
	maxGen uint32
}

// New 创建容量为 capacity、代号空间为 genBits 位的池。
// 代号取值范围为 [1, 1<<genBits - 1]。
func New(capacity int, genBits uint8) *Pool {
	if capacity < 0 {
		panic("slotpool: negative capacity")
	}
	if genBits == 0 || genBits > 31 {
		panic("slotpool: genBits must be in [1, 31]")
	}
	p := &Pool{
		slots:  make([]slot, capacity),
		maxGen: 1<<uint(genBits) - 1,
	}
	for i := range p.slots {
		p.slots[i].gen = 1
		p.free = append(p.free, uint32(i))
	}
	return p
}

// Acquire 借出一个槽，返回其句柄；池耗尽时返回 ErrPoolExhausted。
func (p *Pool) Acquire() (Handle, error) {
	n := len(p.free)
	if n == 0 {
		return Handle{}, ErrPoolExhausted
	}
	idx := p.free[n-1]
	p.free = p.free[:n-1]
	s := &p.slots[idx]
	s.state = stateBorrowed
	s.pendingDelete = false
	return Handle{Slot: idx, Gen: s.gen}, nil
}

// Delete 标记句柄对应槽为待回收。借用期间仅标记，槽仍归调用者占用，
// 须待 Release 后才真正回收复用。重复 Delete 幂等。
func (p *Pool) Delete(h Handle) error {
	s, err := p.lookup(h)
	if err != nil {
		return err
	}
	s.pendingDelete = true
	return nil
}

// Release 释放句柄。槽代号递增后回到空闲链表等待复用，旧句柄从此失效；
// 若代号溢出（超过 maxGen），该槽永久退休，不再参与分配。
func (p *Pool) Release(h Handle) error {
	s, err := p.lookup(h)
	if err != nil {
		return err
	}
	s.gen++
	s.pendingDelete = false
	if s.gen > p.maxGen {
		s.state = stateRetired
		return nil
	}
	s.state = stateFree
	p.free = append(p.free, h.Slot)
	return nil
}

// Alive 报告句柄当前是否有效（对应槽处于借用状态且代号一致）。
func (p *Pool) Alive(h Handle) bool {
	_, err := p.lookup(h)
	return err == nil
}

// Stats 返回空闲、借用中、已退休的槽数量。
func (p *Pool) Stats() (free, borrowed, retired int) {
	for i := range p.slots {
		switch p.slots[i].state {
		case stateFree:
			free++
		case stateBorrowed:
			borrowed++
		case stateRetired:
			retired++
		}
	}
	return free, borrowed, retired
}

func (p *Pool) lookup(h Handle) (*slot, error) {
	if h.Gen == 0 || h.Slot >= uint32(len(p.slots)) {
		return nil, ErrInvalidHandle
	}
	s := &p.slots[h.Slot]
	if s.state != stateBorrowed || s.gen != h.Gen {
		return nil, ErrInvalidHandle
	}
	return s, nil
}
