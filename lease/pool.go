// Package lease 实现分布式并发任务许可证配额租约池与弹性借贷还款管理器。
//
// 核心能力：
//   - 总配额固定的租约池，各业务线按需租赁指定数量的配额并承诺借期；
//   - 超期未归还的配额由协调器强制回收（Revoke），并输出违约惩罚审计日志；
//   - 高优先级业务可申请插队抢占低优先级业务的租约。
package lease

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Priority 租约优先级，数值越大优先级越高。
type Priority int

const (
	PriorityLow Priority = iota
	PriorityNormal
	PriorityHigh
)

func (p Priority) String() string {
	switch p {
	case PriorityLow:
		return "LOW"
	case PriorityNormal:
		return "NORMAL"
	case PriorityHigh:
		return "HIGH"
	default:
		return fmt.Sprintf("PRIORITY(%d)", int(p))
	}
}

// AuditEventType 审计事件类型。
type AuditEventType string

const (
	EventGrant   AuditEventType = "GRANT"          // 租约发放
	EventReturn  AuditEventType = "RETURN"         // 主动归还
	EventRevoke  AuditEventType = "REVOKE_EXPIRED" // 超期强制回收
	EventPreempt AuditEventType = "PREEMPT"        // 高优插队抢占
	EventPenalty AuditEventType = "PENALTY"        // 违约惩罚
)

// AuditEvent 配额审计日志条目。
type AuditEvent struct {
	Time    time.Time
	Type    AuditEventType
	Tenant  string
	LeaseID string
	Count   int
	Detail  string
}

func (e AuditEvent) String() string {
	return fmt.Sprintf("[%s] %-14s tenant=%-8s lease=%-9s count=%-3d %s",
		e.Time.Format("15:04:05.000"), e.Type, e.Tenant, e.LeaseID, e.Count, e.Detail)
}

// Lease 一份配额租约。
type Lease struct {
	ID        string
	Tenant    string
	Count     int
	Priority  Priority
	GrantedAt time.Time
	ExpiresAt time.Time
	Returned  bool
	Revoked   bool
}

// Active 租约是否仍占用配额。
func (l *Lease) Active() bool { return !l.Returned && !l.Revoked }

// Expired 租约是否已过承诺借期。
func (l *Lease) Expired(now time.Time) bool { return !now.Before(l.ExpiresAt) }

var (
	ErrInvalidCount      = errors.New("lease: count must be between 1 and total quota")
	ErrInsufficientQuota = errors.New("lease: insufficient quota")
	ErrLeaseNotFound     = errors.New("lease: lease not found")
	ErrLeaseClosed       = errors.New("lease: lease already returned or revoked")
)

// Pool 租约配额池（弹性借贷还款管理器）。
type Pool struct {
	mu      sync.Mutex
	total   int
	leases  map[string]*Lease
	seq     int64
	audit   []AuditEvent
	now     func() time.Time
	onEvent func(AuditEvent)
}

// Option Pool 可选配置。
type Option func(*Pool)

// WithClock 注入时钟（测试用）。
func WithClock(now func() time.Time) Option {
	return func(p *Pool) { p.now = now }
}

// WithAuditHook 注册审计事件回调（如落盘、上报）。
func WithAuditHook(hook func(AuditEvent)) Option {
	return func(p *Pool) { p.onEvent = hook }
}

// NewPool 创建总配额为 total 的租约池。
func NewPool(total int, opts ...Option) *Pool {
	p := &Pool{total: total, leases: make(map[string]*Lease), now: time.Now}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Total 总配额。
func (p *Pool) Total() int { return p.total }

// Available 当前可借配额。
func (p *Pool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.availableLocked()
}

func (p *Pool) availableLocked() int {
	used := 0
	for _, l := range p.leases {
		if l.Active() {
			used += l.Count
		}
	}
	return p.total - used
}

// Grant 为 tenant 租赁 count 个配额，承诺借期 ttl。
//
// 配额不足时按以下顺序弹性调度：
//  1. 强制回收所有超期未归还的租约（Revoke）；
//  2. 若仍不足且请求方优先级更高，则抢占优先级更低的在租租约（从最低优先级、
//     最早到期者开始）；
//  3. 仍不足则返回 ErrInsufficientQuota。
func (p *Pool) Grant(tenant string, count int, ttl time.Duration, prio Priority) (*Lease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if count <= 0 || count > p.total {
		return nil, ErrInvalidCount
	}

	now := p.now()
	if p.availableLocked() < count {
		p.revokeExpiredLocked(now)
	}
	if p.availableLocked() < count {
		p.preemptLocked(count, prio, now)
	}
	if p.availableLocked() < count {
		return nil, ErrInsufficientQuota
	}

	p.seq++
	l := &Lease{
		ID:        fmt.Sprintf("L%04d", p.seq),
		Tenant:    tenant,
		Count:     count,
		Priority:  prio,
		GrantedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	p.leases[l.ID] = l
	p.emitLocked(AuditEvent{
		Time: now, Type: EventGrant, Tenant: tenant, LeaseID: l.ID, Count: count,
		Detail: fmt.Sprintf("priority=%s ttl=%s expires=%s", prio, ttl, l.ExpiresAt.Format("15:04:05.000")),
	})
	return l, nil
}

// Return 主动归还租约（还款）。逾期归还会追加违约惩罚审计。
func (p *Pool) Return(leaseID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	l, ok := p.leases[leaseID]
	if !ok {
		return ErrLeaseNotFound
	}
	if !l.Active() {
		return ErrLeaseClosed
	}
	now := p.now()
	l.Returned = true
	p.emitLocked(AuditEvent{
		Time: now, Type: EventReturn, Tenant: l.Tenant, LeaseID: l.ID, Count: l.Count,
		Detail: fmt.Sprintf("released %d quota back to pool", l.Count),
	})
	if l.Expired(now) {
		p.emitPenaltyLocked(l, now, "late return")
	}
	return nil
}

// RevokeExpired 强制回收所有超期未归还的租约（协调器巡检入口）。
// 返回本次被吊销的租约。
func (p *Pool) RevokeExpired() []*Lease {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.revokeExpiredLocked(p.now())
}

func (p *Pool) revokeExpiredLocked(now time.Time) []*Lease {
	var revoked []*Lease
	for _, l := range p.leases {
		if l.Active() && l.Expired(now) {
			l.Revoked = true
			revoked = append(revoked, l)
			p.emitLocked(AuditEvent{
				Time: now, Type: EventRevoke, Tenant: l.Tenant, LeaseID: l.ID, Count: l.Count,
				Detail: fmt.Sprintf("force-revoked: overdue %s beyond promised ttl", now.Sub(l.ExpiresAt).Round(time.Millisecond)),
			})
			p.emitPenaltyLocked(l, now, "lease expired without return")
		}
	}
	return revoked
}

// preemptLocked 为高优请求抢占低优在租租约，直到凑齐 count 个配额。
func (p *Pool) preemptLocked(count int, prio Priority, now time.Time) {
	var candidates []*Lease
	for _, l := range p.leases {
		if l.Active() && l.Priority < prio {
			candidates = append(candidates, l)
		}
	}
	// 从最低优先级、最早到期的租约开始抢占。
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].ExpiresAt.Before(candidates[j].ExpiresAt)
	})
	for _, l := range candidates {
		if p.availableLocked() >= count {
			break
		}
		l.Revoked = true
		p.emitLocked(AuditEvent{
			Time: now, Type: EventPreempt, Tenant: l.Tenant, LeaseID: l.ID, Count: l.Count,
			Detail: fmt.Sprintf("preempted by higher priority request (victim=%s)", l.Priority),
		})
	}
}

// emitPenaltyLocked 输出配额违约惩罚审计日志。
func (p *Pool) emitPenaltyLocked(l *Lease, now time.Time, reason string) {
	overdue := now.Sub(l.ExpiresAt)
	if overdue < 0 {
		overdue = 0
	}
	p.emitLocked(AuditEvent{
		Time: now, Type: EventPenalty, Tenant: l.Tenant, LeaseID: l.ID, Count: l.Count,
		Detail: fmt.Sprintf("penalty: reason=%q overdue=%s penalty_score=%d",
			reason, overdue.Round(time.Millisecond), l.Count*int(overdue/time.Millisecond+1)),
	})
}

func (p *Pool) emitLocked(e AuditEvent) {
	p.audit = append(p.audit, e)
	if p.onEvent != nil {
		p.onEvent(e)
	}
}

// Audit 返回审计日志副本。
func (p *Pool) Audit() []AuditEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]AuditEvent, len(p.audit))
	copy(out, p.audit)
	return out
}

// Lease 查询租约快照。
func (p *Pool) Lease(leaseID string) (Lease, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.leases[leaseID]
	if !ok {
		return Lease{}, false
	}
	return *l, true
}
