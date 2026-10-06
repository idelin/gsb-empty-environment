package pool

import (
	"fmt"
	"testing"
)

// TestLifecycle 走一遍完整生命周期：借用期间删除只标记待回收，
// 最后一次释放才回收复用，旧句柄随之失效。
func TestLifecycle(t *testing.T) {
	p := New(2, 2)

	h0, ok := p.Create()
	if !ok || h0 != (Handle{Slot: 0, Gen: 0}) {
		t.Fatalf("Create = %v,%v", h0, ok)
	}
	h1, ok := p.Borrow(h0)
	if !ok {
		t.Fatal("Borrow failed")
	}

	if !p.Delete(h0) {
		t.Fatal("Delete failed")
	}
	// 仍有人借用：槽不得复用，Create 只能给槽 1。
	h2, ok := p.Create()
	if !ok || h2.Slot != 1 {
		t.Fatalf("slot reused while borrowed: %v,%v", h2, ok)
	}
	// 待回收期间句柄仍有效，但不接受新借用。
	if !p.Valid(h0) {
		t.Fatal("pending-reclaim handle should stay valid")
	}
	if _, ok := p.Borrow(h0); ok {
		t.Fatal("borrow of pending-reclaim object should fail")
	}

	if !p.Release(h0) {
		t.Fatal("Release failed")
	}
	if !p.Valid(h1) {
		t.Fatal("slot reclaimed before last release")
	}
	if !p.Release(h1) {
		t.Fatal("last Release failed")
	}
	// 最后一次释放触发回收：旧句柄全部失效。
	if p.Valid(h0) || p.Valid(h1) {
		t.Fatal("stale handles still valid after reclaim")
	}

	h3, ok := p.Create()
	if !ok || h3 != (Handle{Slot: 0, Gen: 1}) {
		t.Fatalf("reuse should bump gen: %v,%v", h3, ok)
	}
	// 旧句柄的所有操作都必须失败。
	if p.Delete(h0) || p.Release(h0) {
		t.Fatal("stale handle accepted")
	}
	if _, ok := p.Borrow(h0); ok {
		t.Fatal("stale handle borrowed")
	}
}

// TestNoWraparound 代号到达上限后槽永久退休，绝不绕回 0。
func TestNoWraparound(t *testing.T) {
	const maxGen = 2
	p := New(1, maxGen)

	lastGen := -1
	creates := 0
	for i := 0; i < 10; i++ {
		h, ok := p.Create()
		if !ok {
			break
		}
		creates++
		if h.Gen <= lastGen {
			t.Fatalf("gen wrapped or stalled: %d after %d", h.Gen, lastGen)
		}
		lastGen = h.Gen
		if !p.Delete(h) || !p.Release(h) {
			t.Fatal("delete/release failed")
		}
	}
	if creates != maxGen+1 {
		t.Fatalf("slot served %d generations, want %d", creates, maxGen+1)
	}
	if _, ok := p.Create(); ok {
		t.Fatal("retired slot handed out again")
	}
	if p.Valid(Handle{Slot: 0, Gen: maxGen}) {
		t.Fatal("handle of retired slot still valid")
	}
}

// TestEdgeCases 越界句柄与重复删除。
func TestEdgeCases(t *testing.T) {
	p := New(1, 1)
	for _, h := range []Handle{{-1, 0}, {1, 0}, {0, -1}, {0, 2}} {
		if p.Valid(h) {
			t.Fatalf("out-of-range handle %v valid", h)
		}
		if p.Delete(h) || p.Release(h) {
			t.Fatalf("out-of-range handle %v accepted", h)
		}
	}
	h, _ := p.Create()
	if !p.Delete(h) {
		t.Fatal("first Delete failed")
	}
	if p.Delete(h) {
		t.Fatal("double Delete accepted")
	}
	if !p.Release(h) { // 最后一个引用：触发回收
		t.Fatal("last Release failed")
	}
	if p.Valid(h) {
		t.Fatal("stale handle valid after reclaim")
	}

	// 无人借用时删除：立即回收，句柄当场失效。
	h2, _ := p.Create()
	if !p.Release(h2) {
		t.Fatal("Release failed")
	}
	if !p.Valid(h2) {
		t.Fatal("idle live object should stay valid")
	}
	if !p.Delete(h2) {
		t.Fatal("Delete of idle object failed")
	}
	if p.Valid(h2) {
		t.Fatal("handle valid after immediate reclaim")
	}
	if p.Release(h2) {
		t.Fatal("Release after immediate reclaim accepted")
	}
}

// ---- 穷举测试：小池 + 小代号空间，遍历所有可达状态 ----

// mslot/model 是规格的直接复述，作为对照模型。
type mslot struct {
	gen     int
	refs    int
	live    bool
	deleted bool
	retired bool
}

type model struct {
	maxGen int
	slots  []mslot
}

func newModel(size, maxGen int) *model {
	return &model{maxGen: maxGen, slots: make([]mslot, size)}
}

func (m *model) clone() *model {
	c := &model{maxGen: m.maxGen, slots: make([]mslot, len(m.slots))}
	copy(c.slots, m.slots)
	return c
}

func (m *model) slot(h Handle) *mslot {
	if h.Slot < 0 || h.Slot >= len(m.slots) {
		return nil
	}
	s := &m.slots[h.Slot]
	if !s.live || s.gen != h.Gen {
		return nil
	}
	return s
}

func (m *model) valid(h Handle) bool { return m.slot(h) != nil }

func (m *model) create() (Handle, bool) {
	for i := range m.slots {
		s := &m.slots[i]
		if s.live || s.retired {
			continue
		}
		s.live = true
		s.refs = 1
		return Handle{Slot: i, Gen: s.gen}, true
	}
	return Handle{}, false
}

func (m *model) borrow(h Handle) (Handle, bool) {
	s := m.slot(h)
	if s == nil || s.deleted {
		return Handle{}, false
	}
	s.refs++
	return h, true
}

func (m *model) delete(h Handle) bool {
	s := m.slot(h)
	if s == nil || s.deleted {
		return false
	}
	s.deleted = true
	if s.refs == 0 {
		m.reclaim(h.Slot)
	}
	return true
}

func (m *model) release(h Handle) bool {
	s := m.slot(h)
	if s == nil || s.refs == 0 {
		return false
	}
	s.refs--
	if s.refs == 0 && s.deleted {
		m.reclaim(h.Slot)
	}
	return true
}

func (m *model) reclaim(i int) {
	s := &m.slots[i]
	s.live = false
	s.deleted = false
	s.refs = 0
	if s.gen == m.maxGen {
		s.retired = true
		return
	}
	s.gen++
}

func (p *Pool) clone() *Pool {
	c := &Pool{maxGen: p.maxGen, slots: make([]slot, len(p.slots))}
	copy(c.slots, p.slots)
	return c
}

// checkAgree 校验池与模型一致，并检查与模型无关的规格级不变量。
func checkAgree(t *testing.T, p *Pool, m *model) {
	t.Helper()
	for i := range p.slots {
		ps, ms := p.slots[i], m.slots[i]
		if ps.gen != ms.gen || ps.refs != ms.refs ||
			ps.live != ms.live || ps.deleted != ms.deleted ||
			ps.retired != ms.retired {
			t.Fatalf("slot %d diverged: pool=%+v model=%+v", i, ps, ms)
		}
		if ps.gen < 0 || ps.gen > p.maxGen {
			t.Fatalf("slot %d gen %d out of range (wraparound?)", i, ps.gen)
		}
		if ps.refs < 0 {
			t.Fatalf("slot %d refs %d < 0", i, ps.refs)
		}
		if ps.retired && ps.live {
			t.Fatalf("slot %d retired but live", i)
		}
		if ps.retired && ps.gen != p.maxGen {
			t.Fatalf("slot %d retired at gen %d, want %d", i, ps.gen, p.maxGen)
		}
		if ps.live && ps.deleted && ps.refs == 0 {
			t.Fatalf("slot %d pending reclaim with no refs: should be reclaimed", i)
		}
	}
	// 对所有可能的句柄（含越界），Valid 必须与模型一致：
	// 复用后旧代号失效，退休后永不复效。
	for s := -1; s <= len(p.slots); s++ {
		for g := -1; g <= p.maxGen+1; g++ {
			h := Handle{Slot: s, Gen: g}
			if got, want := p.Valid(h), m.valid(h); got != want {
				t.Fatalf("Valid(%v) = %v, model says %v", h, got, want)
			}
		}
	}
}

// TestExhaustive 在 2 槽、代号空间 0..2 的池上穷举所有
// 创建/借用/删除/释放序列（深度 20，按 (状态, 剩余深度) 去重），
// 逐步与模型对照。深度 20 足以让两个槽都走完 3 代并退休。
func TestExhaustive(t *testing.T) {
	const (
		size   = 2
		maxGen = 2
		depth  = 20
	)
	// seen 记录每个状态探索过的最大剩余深度；只剪更不深的重访，
	// 否则先到的浅分支会截断后到的深分支。
	seen := make(map[string]int)
	sawRetired := false
	sawAllRetired := false

	var explore func(p *Pool, m *model, d int)
	explore = func(p *Pool, m *model, d int) {
		key := fmt.Sprint(p.slots)
		if seen[key] >= d {
			return
		}
		seen[key] = d
		checkAgree(t, p, m)
		allRetired := true
		for _, s := range p.slots {
			if s.retired {
				sawRetired = true
			} else {
				allRetired = false
			}
		}
		if allRetired {
			sawAllRetired = true
		}
		if d == 0 {
			return
		}

		// 每个分支都在克隆上操作，并比较池与模型的返回值。
		{
			p2, m2 := p.clone(), m.clone()
			hp, okp := p2.Create()
			hm, okm := m2.create()
			if hp != hm || okp != okm {
				t.Fatalf("Create: pool=%v,%v model=%v,%v", hp, okp, hm, okm)
			}
			explore(p2, m2, d-1)
		}
		// 调用者可能持有的句柄：每个槽 × 代号空间中每个值。
		for s := 0; s < size; s++ {
			for g := 0; g <= maxGen; g++ {
				h := Handle{Slot: s, Gen: g}
				{
					p2, m2 := p.clone(), m.clone()
					hp, okp := p2.Borrow(h)
					hm, okm := m2.borrow(h)
					if hp != hm || okp != okm {
						t.Fatalf("Borrow(%v): pool=%v,%v model=%v,%v", h, hp, okp, hm, okm)
					}
					explore(p2, m2, d-1)
				}
				{
					p2, m2 := p.clone(), m.clone()
					if okp, okm := p2.Delete(h), m2.delete(h); okp != okm {
						t.Fatalf("Delete(%v): pool=%v model=%v", h, okp, okm)
					}
					explore(p2, m2, d-1)
				}
				{
					p2, m2 := p.clone(), m.clone()
					if okp, okm := p2.Release(h), m2.release(h); okp != okm {
						t.Fatalf("Release(%v): pool=%v model=%v", h, okp, okm)
					}
					explore(p2, m2, d-1)
				}
			}
		}
	}
	explore(New(size, maxGen), newModel(size, maxGen), depth)
	if !sawRetired || !sawAllRetired {
		t.Fatalf("exploration never retired slots: retired=%v allRetired=%v", sawRetired, sawAllRetired)
	}
	t.Logf("explored %d reachable states", len(seen))
}
