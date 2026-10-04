// Package multires 提供多资源“全有或全无”(all-or-nothing) 领取。
//
// 一个作业需要的全部资源要么一次性全部领到，要么整体排队等待，
// 绝不会出现“拿到一半后阻塞”的部分占用状态。
//
// 调度策略：按队列次序扫描，优先满足可满足的请求；不可满足的队首
// 每被后来的请求“跳过”（绕过领取）一次就累计一次，累计达到
// maxSkips（默认 3）次后，队首成为屏障，禁止任何后续请求再跳过它，
// 直到它被满足或被取消。这保证了有界等待，避免队首饥饿。
package multires

import (
	"context"
	"errors"
	"sync"
)

// ErrInvalidDemand 表示需求量非法（数量为 0 或负数）。
var ErrInvalidDemand = errors.New("multires: demand quantities must be positive")

// DefaultMaxSkips 是队首请求允许被跳过的默认最大次数。
const DefaultMaxSkips = 3

// Demand 描述一次领取对各类资源的需求量。
type Demand map[string]int64

// Pool 是多资源领取池。零值不可用，请使用 New 构造。
type Pool struct {
	mu        sync.Mutex
	quota     map[string]int64 // 各类资源的总额度
	inUse     map[string]int64 // 已领出（未归还）的数量
	queue     []*waiter        // FIFO 等待队列
	onRelease func(Demand)     // 释放回调，在锁外执行，允许重入
	maxSkips  int              // 队首允许被跳过的最大次数
}

type waiter struct {
	needs   Demand
	ready   chan struct{} // 授权时关闭
	granted bool
	skipped int // 被后来的请求绕过领取的累计次数
}

// New 以给定配额创建领取池。
func New(quota map[string]int64) *Pool {
	p := &Pool{
		quota:    make(map[string]int64, len(quota)),
		inUse:    make(map[string]int64, len(quota)),
		maxSkips: DefaultMaxSkips,
	}
	for r, n := range quota {
		p.quota[r] = n
	}
	return p
}

// Acquire 一次性领取 needs 中的全部资源。
//
// 成功时返回释放函数；释放函数幂等，多次调用只生效一次。
// ctx 取消或超时返回 ctx.Err()，且不会在池中留下任何占用。
// 若在取消的同时恰好被授权，则视为成功（调用方仍须负责释放）。
func (p *Pool) Acquire(ctx context.Context, needs Demand) (func(), error) {
	if err := validate(needs); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w := &waiter{needs: cloneDemand(needs), ready: make(chan struct{})}

	p.mu.Lock()
	p.queue = append(p.queue, w)
	p.scheduleLocked()
	p.mu.Unlock()

	select {
	case <-w.ready:
		return p.releaser(w), nil
	case <-ctx.Done():
		p.mu.Lock()
		if w.granted {
			// 与取消竞争的授权：按成功处理，避免资源泄漏。
			p.mu.Unlock()
			return p.releaser(w), nil
		}
		p.removeLocked(w)
		// 被取消的可能是屏障队首，移除后需重新调度。
		p.scheduleLocked()
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

// SetQuota 调整某类资源的总额度。
//
// 调大会唤醒可被满足的等待者；调小不抢占已领出的资源，
// 可用量（quota-inUse）可能暂时为负，仅阻止新的授权，
// 直到持有者归还使占用回落到配额之内。
func (p *Pool) SetQuota(resource string, quota int64) {
	p.mu.Lock()
	p.quota[resource] = quota
	p.scheduleLocked()
	p.mu.Unlock()
}

// SetMaxSkips 设置队首允许被跳过的最大次数（<=0 表示严格 FIFO，不允许跳过）。
func (p *Pool) SetMaxSkips(n int) {
	p.mu.Lock()
	p.maxSkips = n
	p.scheduleLocked()
	p.mu.Unlock()
}

// SetOnRelease 设置释放回调。回调在锁外同步执行，允许重入调用
// Pool 的任何方法（包括 Acquire/Release）；回调不应阻塞，
// 否则会拖慢调用释放的协程。回调触发前等待队列已被重新调度，
// 即等待者对释放出的资源有优先权。
func (p *Pool) SetOnRelease(cb func(Demand)) {
	p.mu.Lock()
	p.onRelease = cb
	p.mu.Unlock()
}

// Stats 是池状态的快照。
type Stats struct {
	Quota     map[string]int64
	InUse     map[string]int64
	Available map[string]int64 // Quota-InUse，配额缩减后可能为负
	Queued    int              // 排队中的请求数
}

// Stats 返回当前状态快照。
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := Stats{
		Quota:     make(map[string]int64, len(p.quota)),
		InUse:     make(map[string]int64, len(p.inUse)),
		Available: make(map[string]int64, len(p.quota)),
		Queued:    len(p.queue),
	}
	for r, n := range p.quota {
		s.Quota[r] = n
		s.Available[r] = n - p.inUse[r]
	}
	for r, n := range p.inUse {
		s.InUse[r] = n
	}
	return s
}

// scheduleLocked 按队列次序扫描并授权可满足的请求。
// 队首不可满足且已被跳过 maxSkips 次时，扫描停止（队首屏障）。
// 调用时必须持有 p.mu。
func (p *Pool) scheduleLocked() {
	i := 0
	for i < len(p.queue) {
		w := p.queue[i]
		if !p.canSatisfyLocked(w.needs) {
			if i == 0 && w.skipped >= p.maxSkips {
				return // 队首屏障：禁止继续跳过
			}
			i++
			continue
		}
		// 授权第 i 个请求：所有排在它前面仍未满足的请求被“跳过”一次。
		for k := 0; k < i; k++ {
			p.queue[k].skipped++
		}
		for r, n := range w.needs {
			p.inUse[r] += n
		}
		copy(p.queue[i:], p.queue[i+1:])
		p.queue[len(p.queue)-1] = nil
		p.queue = p.queue[:len(p.queue)-1]
		w.granted = true
		close(w.ready)
		// 不递增 i：检查新的第 i 个请求。
	}
}

func (p *Pool) canSatisfyLocked(d Demand) bool {
	for r, n := range d {
		if p.quota[r]-p.inUse[r] < n {
			return false
		}
	}
	return true
}

func (p *Pool) removeLocked(w *waiter) {
	for i, q := range p.queue {
		if q == w {
			copy(p.queue[i:], p.queue[i+1:])
			p.queue[len(p.queue)-1] = nil
			p.queue = p.queue[:len(p.queue)-1]
			return
		}
	}
}

// releaser 返回幂等的释放函数。归还资源后先重新调度等待队列，
// 再在锁外触发释放回调，因此回调可以安全地重入本池。
func (p *Pool) releaser(w *waiter) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			for r, n := range w.needs {
				p.inUse[r] -= n
			}
			p.scheduleLocked()
			cb := p.onRelease
			p.mu.Unlock()
			if cb != nil {
				cb(cloneDemand(w.needs))
			}
		})
	}
}

func validate(d Demand) error {
	for _, n := range d {
		if n <= 0 {
			return ErrInvalidDemand
		}
	}
	return nil
}

func cloneDemand(d Demand) Demand {
	c := make(Demand, len(d))
	for r, n := range d {
		c[r] = n
	}
	return c
}
