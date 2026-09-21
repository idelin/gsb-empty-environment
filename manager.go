// Package quota 实现 CPU / IO 两阶段的额度（容量）管理。
//
// 任务按“阶段”依次申请额度：每个阶段都需要先获得该阶段的额度，
// 获得成功后才执行注入的任务函数，函数返回后进入下一阶段。
//
// 关键规则：
//   - 阶段切换是原子的：先释放旧阶段额度，再申请新阶段额度，
//     任务任何时刻至多持有一个阶段的额度，不会“占有旧额度等待新额度”，
//     因此两任务互换阶段（A 持 CPU 等 IO、B 持 IO 等 CPU）不会死锁。
//   - 申请额度期间任务不持有任何额度；此时上下文取消属于“半转换”状态，
//     直接以 context.Canceled（或 DeadlineExceeded）退出，不会泄漏额度。
//   - 容量为 0 的阶段永远无法满足，只有取消才能让等待者退出。
//   - 任务函数返回错误或 panic 都视为异常结束；所有已持有的额度
//     通过 defer 归还，Manager 内部计数恢复初值。panic 会被重新抛出。
package quota

import (
	"context"
	"sync"
)

// Phase 标识一个资源阶段。
type Phase int

const (
	CPU Phase = iota
	IO
)

// TaskFunc 由测试注入的阶段执行函数。
//   - p 为当前正在执行的阶段；
//   - 返回 nil 表示该阶段正常完成，继续下一阶段；
//   - 返回非 nil 表示任务异常结束，额度归还，错误由 Run 返回。
type TaskFunc func(p Phase) error

// Manager 管理各阶段的额度。零值不可用，必须用 NewManager 构造。
type Manager struct {
	mu    sync.Mutex
	slots map[Phase]chan struct{} // 容量即该阶段额度；持一个 token 表示占一份额度
	used  map[Phase]int           // 已分配额度（用于快照 / 调试断言）
}

// NewManager 按给定容量构造 Manager，容量必须非负。
func NewManager(capacities map[Phase]int) *Manager {
	m := &Manager{
		slots: make(map[Phase]chan struct{}, len(capacities)),
		used:  make(map[Phase]int, len(capacities)),
	}
	for p, c := range capacities {
		if c < 0 {
			panic("quota: negative capacity")
		}
		m.slots[p] = make(chan struct{}, c)
		m.used[p] = 0
	}
	return m
}

// acquire 申请指定阶段的一份额度。
// 申请期间不持有任何额度；ctx 取消（半转换状态）时直接退出，不泄漏额度。
func (m *Manager) acquire(ctx context.Context, p Phase) error {
	ch, ok := m.slots[p]
	if !ok {
		// 未配置的阶段立即满足（无额度约束），不参与计数。
		return nil
	}
	// 容量为 0 时 ch 为 nil，两个 case 中只有 ctx.Done 可触发，
	// 即只能通过取消退出。
	select {
	case ch <- struct{}{}:
		m.mu.Lock()
		m.used[p]++
		m.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release 归还指定阶段的一份额度。
func (m *Manager) release(p Phase) {
	ch, ok := m.slots[p]
	if !ok {
		return
	}
	select {
	case <-ch:
		m.mu.Lock()
		m.used[p]--
		m.mu.Unlock()
	default:
		panic("quota: releasing unacquired slot")
	}
}

// Usage 返回某阶段当前已占用的额度（快照）。
func (m *Manager) Usage(p Phase) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.used[p]
}

// Run 同步运行一个任务，依次经历 phases 中的各阶段。
// 每个阶段先申请额度，申请成功后执行 fn，执行完再原子切换：
// 先释放本阶段额度，再申请下一阶段额度。
//
// 任务函数 fn 的 panic 会在额度归还后重新抛出，调用方可自行 recover。
func (m *Manager) Run(ctx context.Context, phases []Phase, fn TaskFunc) (err error) {
	var held Phase = -1 // 当前持有的阶段；-1 表示未持有（含半转换状态）
	defer func() {
		if held != -1 {
			m.release(held)
			held = -1
		}
		if r := recover(); r != nil {
			panic(r)
		}
	}()

	for _, p := range phases {
		// 阶段切换原子点：先释放旧额度……
		if held != -1 {
			m.release(held)
			held = -1
		}
		// ……再申请新额度。此等待区间为“半转换”状态：不持有任何额度，
		// 取消时直接退出，defer 不会重复释放。
		if err = m.acquire(ctx, p); err != nil {
			return err
		}
		held = p

		if err = fn(p); err != nil {
			return err // defer 归还 held 额度
		}
	}
	return nil
}
