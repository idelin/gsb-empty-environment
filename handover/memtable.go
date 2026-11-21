package handover

import (
	"fmt"
	"sort"
	"sync"
)

// MemTable 是 Executor 的内存参考实现，带互斥锁，可用于宿主适配与测试。
type MemTable struct {
	mu      sync.Mutex
	handles map[int]Handle
}

// NewMemTable 从句柄表快照构造内存表。
func NewMemTable(tab *Table) *MemTable {
	mt := &MemTable{handles: make(map[int]Handle, len(tab.Handles))}
	for _, h := range tab.Handles {
		mt.handles[h.Slot] = h
	}
	return mt
}

// HandleAt 实现 LiveView。
func (m *MemTable) HandleAt(slot int) (Handle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.handles[slot]
	return h, ok
}

// Snapshot 返回当前所有句柄（按槽号升序）。
func (m *MemTable) Snapshot() []Handle {
	m.mu.Lock()
	defer m.mu.Unlock()
	slots := make([]int, 0, len(m.handles))
	for s := range m.handles {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	out := make([]Handle, 0, len(slots))
	for _, s := range slots {
		out = append(out, m.handles[s])
	}
	return out
}

func (m *MemTable) Swap(a, b int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ha, oka := m.handles[a]
	hb, okb := m.handles[b]
	if !oka || !okb {
		return fmt.Errorf("swap slots %d,%d must both be occupied", a, b)
	}
	m.handles[a] = withSlot(hb, a)
	m.handles[b] = withSlot(ha, b)
	return nil
}

func (m *MemTable) ParSwap(pairs [][2]int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[int]struct{})
	for _, p := range pairs {
		if _, ok := m.handles[p[0]]; !ok {
			return fmt.Errorf("parswap slot %d empty", p[0])
		}
		if _, ok := m.handles[p[1]]; !ok {
			return fmt.Errorf("parswap slot %d empty", p[1])
		}
		for _, s := range p {
			if _, dup := seen[s]; dup {
				return fmt.Errorf("parswap pairs overlap at %d", s)
			}
			seen[s] = struct{}{}
		}
	}
	for _, p := range pairs {
		ha, hb := m.handles[p[0]], m.handles[p[1]]
		m.handles[p[0]] = withSlot(hb, p[0])
		m.handles[p[1]] = withSlot(ha, p[1])
	}
	return nil
}

func (m *MemTable) Move(src, dst int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.handles[src]
	if !ok {
		return fmt.Errorf("move source %d empty", src)
	}
	if _, occupied := m.handles[dst]; occupied {
		return fmt.Errorf("move destination %d occupied", dst)
	}
	delete(m.handles, src)
	m.handles[dst] = withSlot(h, dst)
	return nil
}

func (m *MemTable) SetCloexec(slot int, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.handles[slot]
	if !ok {
		return fmt.Errorf("setcloexec slot %d empty", slot)
	}
	h.Cloexec = on
	m.handles[slot] = h
	return nil
}

func (m *MemTable) Close(slot int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.handles[slot]; !ok {
		return fmt.Errorf("close slot %d already empty", slot)
	}
	delete(m.handles, slot)
	return nil
}

func (m *MemTable) restore(snapshot map[int]slotSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handles = make(map[int]Handle, len(snapshot))
	for slot, s := range snapshot {
		if !s.present {
			continue
		}
		m.handles[slot] = s.handle
	}
}

func withSlot(h Handle, slot int) Handle {
	h.Slot = slot
	return h
}
