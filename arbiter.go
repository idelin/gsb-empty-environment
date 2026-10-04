// Package leasearb 实现带代次（fencing generation）的租约写入仲裁。
//
// 问题背景：租约可以续租或转交；新持有者开始工作后，旧持有者的请求
// （在途写入、重试、延迟确认）仍可能返回。仲裁器需要区分：
//
//   - 重复请求：同一持有者同一代次内相同 requestID 的重试 —— 幂等放行；
//   - 过期持有者：代次落后于当前代次的写入 —— 拒绝（fencing）；
//   - 续租复活：旧代次的续租不允许把旧持有者"复活"。
package leasearb

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Lease 描述一次租约授予。Generation 是单调递增的代次（fencing token），
// 每次授予（Acquire/Transfer）都会递增，续租不改变代次。
type Lease struct {
	Holder     string
	Generation uint64
	ExpiresAt  time.Time
}

func (l Lease) String() string {
	return fmt.Sprintf("lease{holder=%s gen=%d exp=%s}",
		l.Holder, l.Generation, l.ExpiresAt.UTC().Format("15:04:05.000"))
}

// Verdict 是写入仲裁的结果。
type Verdict int

const (
	// Accepted 写入被接纳（首次见到该 requestID）。
	Accepted Verdict = iota
	// Duplicate 同一 (generation, requestID) 已被接纳过 —— 重试/延迟确认，幂等成功。
	Duplicate
	// StaleGeneration 代次落后于当前代次 —— 旧持有者的在途写入，拒绝。
	StaleGeneration
	// NotHolder 代次正确但持有者不匹配，拒绝。
	NotHolder
	// LeaseExpired 代次正确但租约已过期（时钟已越过 ExpiresAt），拒绝。
	LeaseExpired
)

func (v Verdict) String() string {
	switch v {
	case Accepted:
		return "Accepted"
	case Duplicate:
		return "Duplicate"
	case StaleGeneration:
		return "StaleGeneration"
	case NotHolder:
		return "NotHolder"
	case LeaseExpired:
		return "LeaseExpired"
	}
	return fmt.Sprintf("Verdict(%d)", int(v))
}

// Admitted 表示写入被接受（Accepted 或 Duplicate 都视为幂等成功）。
func (v Verdict) Admitted() bool { return v == Accepted || v == Duplicate }

var (
	// ErrLeaseHeld 租约仍被他人持有且未过期。
	ErrLeaseHeld = errors.New("leasearb: lease is held by another holder")
	// ErrNotHolder 操作者不是当前持有者。
	ErrNotHolder = errors.New("leasearb: caller is not the current holder")
	// ErrStaleGeneration 代次已过期：续租不能复活旧代次。
	ErrStaleGeneration = errors.New("leasearb: stale generation, cannot resurrect old lease")
)

// Arbiter 是单资源的租约写入仲裁器，并发安全。
type Arbiter struct {
	mu    sync.Mutex
	clock Clock

	lease    Lease
	hasLease bool
	// lastGen 单调递增，即使租约过期也不回退，保证代次永不复用。
	lastGen uint64

	// admitted 记录每一代次已接纳的 requestID，用于去重。
	// 旧代次的记录保留，使得"旧请求延迟返回"能被识别为 Duplicate
	// 而不是被误判为新的非法写入。
	admitted map[uint64]map[string]struct{}
}

func NewArbiter(clock Clock) *Arbiter {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Arbiter{
		clock:    clock,
		admitted: make(map[uint64]map[string]struct{}),
	}
}

func (a *Arbiter) now() time.Time { return a.clock.Now() }

// activeLocked 报告当前租约是否存在且未过期。
func (a *Arbiter) activeLocked() bool {
	return a.hasLease && a.now().Before(a.lease.ExpiresAt)
}

// Snapshot 返回当前租约状态；active=false 表示无租约或已过期。
func (a *Arbiter) Snapshot() (lease Lease, active bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lease, a.activeLocked()
}

// Acquire 申请租约。仅当无租约或租约已过期时授予；授予时代次 +1。
func (a *Arbiter) Acquire(holder string, ttl time.Duration) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeLocked() {
		return Lease{}, ErrLeaseHeld
	}
	a.lastGen++
	a.lease = Lease{Holder: holder, Generation: a.lastGen, ExpiresAt: a.now().Add(ttl)}
	a.hasLease = true
	return a.lease, nil
}

// Transfer 显式交接：from 必须是当前持有者。无论租约是否临近过期，
// 新持有者立即获得新代次，旧持有者的在途写入从此被 fencing 拒绝。
func (a *Arbiter) Transfer(from, to string, ttl time.Duration) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.hasLease || a.lease.Holder != from {
		return Lease{}, ErrNotHolder
	}
	a.lastGen++
	a.lease = Lease{Holder: to, Generation: a.lastGen, ExpiresAt: a.now().Add(ttl)}
	return a.lease, nil
}

// Renew 续租：持有者、代次都必须与当前租约完全一致。
// 代次不匹配（例如已发生交接）时返回 ErrStaleGeneration —— 续租不能复活旧代次。
// 租约已过期但尚未被他人获取时，续租仍可延长当前代次（这不构成复活）。
func (a *Arbiter) Renew(holder string, generation uint64, ttl time.Duration) (Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.hasLease || generation != a.lease.Generation {
		return Lease{}, ErrStaleGeneration
	}
	if a.lease.Holder != holder {
		return Lease{}, ErrNotHolder
	}
	a.lease.ExpiresAt = a.now().Add(ttl)
	return a.lease, nil
}

// AdmitWrite 对一次写入做仲裁。holder/generation 来自写入方持有的租约，
// requestID 由写入方生成、在重试间保持稳定，用于幂等去重。
//
// 判定顺序很关键：先查去重表，使"已被应用过的旧请求延迟返回"报告为
// Duplicate（幂等成功），而不是 StaleGeneration。
func (a *Arbiter) AdmitWrite(holder string, generation uint64, requestID string) Verdict {
	a.mu.Lock()
	defer a.mu.Unlock()

	if reqs, ok := a.admitted[generation]; ok {
		if _, dup := reqs[requestID]; dup {
			return Duplicate
		}
	}

	if !a.hasLease || generation != a.lease.Generation {
		return StaleGeneration
	}
	if !a.now().Before(a.lease.ExpiresAt) {
		return LeaseExpired
	}
	if holder != a.lease.Holder {
		return NotHolder
	}

	reqs, ok := a.admitted[generation]
	if !ok {
		reqs = make(map[string]struct{})
		a.admitted[generation] = reqs
	}
	reqs[requestID] = struct{}{}
	return Accepted
}
