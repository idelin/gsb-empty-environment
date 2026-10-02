// Package leasepool 实现分布式并发任务许可证配额租约池与弹性借贷还款管理器。
//
// 核心能力：
//   - 固定总配额的租约池，业务线按需申请指定数量的配额并承诺借期 (TTL)；
//   - 协调器后台巡检，超期未归还的租约被强制吊销 (Revoke) 并回收配额；
//   - 高优先级请求可插队，并触发对超期租约的抢占式回收；
//   - 全程审计日志（GRANT / RETURN / REVOKE / PENALTY），违约方累计惩罚积分。
package leasepool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrExceedsTotal 表示单次申请数量超过池子总配额。
var ErrExceedsTotal = errors.New("requested count exceeds total quota")

// ErrPoolClosed 表示池子已关闭。
var ErrPoolClosed = errors.New("lease pool is closed")

// Priority 表示申请优先级，数值越大优先级越高。
type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

// EventType 是审计事件类型。
type EventType string

const (
	EventGrant   EventType = "GRANT"   // 租约发放
	EventReturn  EventType = "RETURN"  // 主动归还（还款）
	EventRevoke  EventType = "REVOKE"  // 超期强制吊销
	EventPenalty EventType = "PENALTY" // 违约惩罚
)

// AuditEvent 是一条配额审计日志。
type AuditEvent struct {
	Time    time.Time
	Type    EventType
	Owner   string
	LeaseID int64
	Count   int
	Detail  string
}

func (e AuditEvent) String() string {
	return fmt.Sprintf("[%s] %-7s owner=%-12s lease=#%-3d count=%-3d %s",
		e.Time.Format("15:04:05.000"), e.Type, e.Owner, e.LeaseID, e.Count, e.Detail)
}

// Lease 表示一份已发放的配额租约。
type Lease struct {
	ID        int64
	Owner     string
	Count     int
	Priority  Priority
	GrantedAt time.Time
	ExpiresAt time.Time

	returned bool
	revoked  bool
}

// Active 报告租约是否仍然有效（未归还且未被吊销）。
func (l *Lease) Active() bool { return !l.returned && !l.revoked }

// Revoked 报告租约是否已被协调器强制吊销。
func (l *Lease) Revoked() bool { return l.revoked }

// Pool 是租约配额池（协调器）。
type Pool struct {
	mu            sync.Mutex
	cond          *sync.Cond
	total         int
	available     int
	leases        map[int64]*Lease
	nextID        int64
	waiters       map[Priority]int // 各优先级正在等待的申请数
	audit         []AuditEvent
	penalties     map[string]int // 业务线 -> 违约惩罚积分
	sweepInterval time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
	closed        bool
}

// NewPool 创建总配额为 total 的租约池，并启动协调器巡检协程。
// sweepInterval 为超期租约的强制回收巡检周期。
func NewPool(total int, sweepInterval time.Duration) *Pool {
	p := &Pool{
		total:         total,
		available:     total,
		leases:        make(map[int64]*Lease),
		waiters:       make(map[Priority]int),
		penalties:     make(map[string]int),
		sweepInterval: sweepInterval,
		stopCh:        make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	p.wg.Add(1)
	go p.sweeper()
	return p
}

// Acquire 为 owner 申请 count 个配额，承诺借期 ttl。
// 配额不足时阻塞等待；更高优先级的等待者会优先获得释放出的配额；
// 等待期间超期租约会被强制吊销并回收配额（抢占）。ctx 取消时返回错误。
func (p *Pool) Acquire(ctx context.Context, owner string, count int, ttl time.Duration, prio Priority) (*Lease, error) {
	if count <= 0 {
		return nil, errors.New("count must be positive")
	}
	if count > p.total {
		return nil, ErrExceedsTotal
	}

	// ctx 取消时唤醒等待者重新检查条件。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		case <-done:
		}
	}()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.waiters[prio]++
	defer func() { p.waiters[prio]-- }()

	for {
		if p.closed {
			return nil, ErrPoolClosed
		}
		// 先回收超期租约：高优请求在此触发对违约租约的抢占。
		p.reclaimExpiredLocked(time.Now())
		if p.available >= count && !p.hasHigherWaiterLocked(prio) {
			p.nextID++
			now := time.Now()
			lease := &Lease{
				ID:        p.nextID,
				Owner:     owner,
				Count:     count,
				Priority:  prio,
				GrantedAt: now,
				ExpiresAt: now.Add(ttl),
			}
			p.leases[lease.ID] = lease
			p.available -= count
			p.logLocked(EventGrant, owner, lease.ID, count,
				fmt.Sprintf("ttl=%s priority=%d available=%d", ttl, prio, p.available))
			return lease, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.cond.Wait()
	}
}

// Return 主动归还租约（还款）。已被吊销或已归还的租约返回错误。
func (p *Pool) Return(lease *Lease) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	tracked, ok := p.leases[lease.ID]
	if !ok || tracked != lease {
		return fmt.Errorf("lease #%d does not belong to this pool", lease.ID)
	}
	if lease.revoked {
		return fmt.Errorf("lease #%d already revoked", lease.ID)
	}
	if lease.returned {
		return fmt.Errorf("lease #%d already returned", lease.ID)
	}
	lease.returned = true
	p.available += lease.Count
	p.logLocked(EventReturn, lease.Owner, lease.ID, lease.Count,
		fmt.Sprintf("available=%d", p.available))
	p.cond.Broadcast()
	return nil
}

// Available 返回当前空闲配额数。
func (p *Pool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.available
}

// Penalty 返回某业务线累计的违约惩罚积分。
func (p *Pool) Penalty(owner string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.penalties[owner]
}

// AuditLog 返回完整审计日志的副本。
func (p *Pool) AuditLog() []AuditEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]AuditEvent, len(p.audit))
	copy(out, p.audit)
	return out
}

// Close 关闭池子并停止协调器巡检协程。
func (p *Pool) Close() {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		close(p.stopCh)
		p.cond.Broadcast()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// sweeper 是协调器巡检协程：周期性强制回收超期未归还的租约。
func (p *Pool) sweeper() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case now := <-ticker.C:
			p.mu.Lock()
			p.reclaimExpiredLocked(now)
			p.cond.Broadcast()
			p.mu.Unlock()
		}
	}
}

// reclaimExpiredLocked 吊销所有超期未归还的租约，回收配额并记录违约惩罚审计。
func (p *Pool) reclaimExpiredLocked(now time.Time) {
	for _, l := range p.leases {
		if l.Active() && now.After(l.ExpiresAt) {
			l.revoked = true
			p.available += l.Count
			overdue := now.Sub(l.ExpiresAt).Round(time.Millisecond)
			p.logLocked(EventRevoke, l.Owner, l.ID, l.Count,
				fmt.Sprintf("overdue=%s force-revoked, quota reclaimed (available=%d)", overdue, p.available))
			p.penalties[l.Owner] += l.Count
			p.logLocked(EventPenalty, l.Owner, l.ID, l.Count,
				fmt.Sprintf("violation penalty +%d, total=%d", l.Count, p.penalties[l.Owner]))
		}
	}
}

// hasHigherWaiterLocked 报告是否存在更高优先级的等待者（用于优先级插队）。
func (p *Pool) hasHigherWaiterLocked(prio Priority) bool {
	for wprio, n := range p.waiters {
		if wprio > prio && n > 0 {
			return true
		}
	}
	return false
}

func (p *Pool) logLocked(t EventType, owner string, leaseID int64, count int, detail string) {
	p.audit = append(p.audit, AuditEvent{
		Time:    time.Now(),
		Type:    t,
		Owner:   owner,
		LeaseID: leaseID,
		Count:   count,
		Detail:  detail,
	})
}
