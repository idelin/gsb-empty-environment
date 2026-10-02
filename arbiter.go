package leaseaudit

import (
	"fmt"
	"sync"
	"time"
)

// Arbiter 主租约仲裁器。所有状态变迁与决策都会写入审计追踪器。
// grantDelay 模拟仲裁耗时（如磁盘落盘、多数派确认），它构成竞态窗口：
// 窗口内到达的第二个申请会被拒绝并产生 CONFLICT 证据事件。
type Arbiter struct {
	mu         sync.Mutex
	tracker    *Tracker
	id         string
	leaseID    string
	ttl        time.Duration
	grantDelay time.Duration
	now        func() time.Time

	state  LockState
	holder string
	term   uint64
	expiry time.Time

	pendingReq        Event // 竞态窗口中正在处理的申请
	pendingTransition Event // IDLE->GRANTING 的锁状态变迁事件
	lastGrant         Event // 最近一次授予事件
}

// NewArbiter 创建仲裁器。now 为 nil 时使用真实时钟。
func NewArbiter(tracker *Tracker, leaseID string, ttl, grantDelay time.Duration, now func() time.Time) *Arbiter {
	if now == nil {
		now = time.Now
	}
	return &Arbiter{
		tracker:    tracker,
		id:         "arbiter",
		leaseID:    leaseID,
		ttl:        ttl,
		grantDelay: grantDelay,
		now:        now,
		state:      StateIdle,
	}
}

// State 返回当前锁状态与持有者（用于测试与演示）。
func (a *Arbiter) State() (LockState, string, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state, a.holder, a.term
}

func (a *Arbiter) transitionLocked(to LockState, causedBy ...uint64) Event {
	from := a.state
	a.state = to
	return a.tracker.Record(Event{
		NodeID:   a.id,
		LeaseID:  a.leaseID,
		Type:     EventLockTransition,
		From:     from,
		To:       to,
		Term:     a.term,
		CausedBy: causedBy,
		Detail:   fmt.Sprintf("arbiter lock %s -> %s", from, to),
	})
}

// Acquire 节点申请主租约。返回是否成功及决定性事件（授予或拒绝）。
func (a *Arbiter) Acquire(nodeID string) (bool, Event) {
	req := a.tracker.Record(Event{
		NodeID:  nodeID,
		LeaseID: a.leaseID,
		Type:    EventAcquireRequest,
		Detail:  fmt.Sprintf("node %s requests lease %s", nodeID, a.leaseID),
	})

	a.mu.Lock()
	// 仲裁器收到请求：合并因果上下文
	a.tracker.Merge(a.id, req.Vector)

	if a.state != StateIdle {
		causes := []uint64{req.Seq}
		if a.pendingReq.Seq != 0 {
			causes = append(causes, a.pendingReq.Seq)
		}
		if a.pendingTransition.Seq != 0 {
			causes = append(causes, a.pendingTransition.Seq)
		}
		if a.lastGrant.Seq != 0 {
			causes = append(causes, a.lastGrant.Seq)
		}
		rej := a.tracker.Record(Event{
			NodeID:   a.id,
			LeaseID:  a.leaseID,
			Type:     EventRejected,
			Term:     a.term,
			CausedBy: causes,
			Detail: fmt.Sprintf("rejected %s: lock=%s holder=%q term=%d",
				nodeID, a.state, a.holder, a.term),
		})
		if a.state == StateGranting {
			// 竞态窗口内到达：两份申请因果并发，记录冲突证据
			delta := req.WallTime.Sub(a.pendingReq.WallTime)
			if delta < 0 {
				delta = -delta
			}
			a.tracker.Record(Event{
				NodeID:   a.id,
				LeaseID:  a.leaseID,
				Type:     EventConflict,
				Term:     a.term,
				CausedBy: []uint64{a.pendingReq.Seq, req.Seq, rej.Seq},
				Detail: fmt.Sprintf("race detected: requests #%d(%s) and #%d(%s) arrived %s apart within granting window",
					a.pendingReq.Seq, a.pendingReq.NodeID, req.Seq, nodeID, delta),
			})
		}
		a.mu.Unlock()
		return false, rej
	}

	a.pendingReq = req
	a.pendingTransition = a.transitionLocked(StateGranting, req.Seq)
	a.mu.Unlock()

	// 仲裁耗时（持锁窗口外），期间锁状态对外呈现 GRANTING
	if a.grantDelay > 0 {
		time.Sleep(a.grantDelay)
	}

	a.mu.Lock()
	a.term++
	a.holder = nodeID
	a.expiry = a.now().Add(a.ttl)
	a.transitionLocked(StateHeld, req.Seq)
	grant := a.tracker.Record(Event{
		NodeID:   a.id,
		LeaseID:  a.leaseID,
		Type:     EventAcquireGranted,
		Term:     a.term,
		CausedBy: []uint64{req.Seq},
		Detail:   fmt.Sprintf("lease granted to %s term=%d ttl=%s", nodeID, a.term, a.ttl),
	})
	a.lastGrant = grant
	a.pendingReq = Event{}
	a.pendingTransition = Event{}
	a.mu.Unlock()
	return true, grant
}

// Renew 持有者续期。非持有者或已超时的续期会被拒绝。
func (a *Arbiter) Renew(nodeID string) (bool, Event) {
	req := a.tracker.Record(Event{
		NodeID:  nodeID,
		LeaseID: a.leaseID,
		Type:    EventRenewRequest,
		Detail:  fmt.Sprintf("node %s requests renewal", nodeID),
	})

	a.mu.Lock()
	defer a.mu.Unlock()
	a.tracker.Merge(a.id, req.Vector)

	if a.state != StateHeld || a.holder != nodeID {
		return false, a.tracker.Record(Event{
			NodeID:   a.id,
			LeaseID:  a.leaseID,
			Type:     EventRejected,
			Term:     a.term,
			CausedBy: []uint64{req.Seq},
			Detail: fmt.Sprintf("renew rejected for %s: lock=%s holder=%q",
				nodeID, a.state, a.holder),
		})
	}
	if !a.now().Before(a.expiry) {
		a.expireLocked(req.Seq)
		return false, a.tracker.Record(Event{
			NodeID:   a.id,
			LeaseID:  a.leaseID,
			Type:     EventRejected,
			Term:     a.term,
			CausedBy: []uint64{req.Seq},
			Detail:   fmt.Sprintf("renew rejected for %s: lease already expired", nodeID),
		})
	}

	a.expiry = a.now().Add(a.ttl)
	return true, a.tracker.Record(Event{
		NodeID:   a.id,
		LeaseID:  a.leaseID,
		Type:     EventRenewed,
		Term:     a.term,
		CausedBy: []uint64{req.Seq},
		Detail:   fmt.Sprintf("lease renewed for %s term=%d new expiry in %s", nodeID, a.term, a.ttl),
	})
}

// Handoff 持有者将租约移交给另一节点（任期递增，不发生空闲窗口）。
func (a *Arbiter) Handoff(from, to string) (bool, Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.state != StateHeld || a.holder != from {
		return false, a.tracker.Record(Event{
			NodeID:  a.id,
			LeaseID: a.leaseID,
			Type:    EventRejected,
			Term:    a.term,
			Detail: fmt.Sprintf("handoff rejected: %s is not the holder (lock=%s holder=%q)",
				from, a.state, a.holder),
		})
	}

	a.transitionLocked(StateReleasing, a.lastGrant.Seq)
	a.holder = to
	a.term++
	a.expiry = a.now().Add(a.ttl)
	a.transitionLocked(StateHeld, a.lastGrant.Seq)
	ev := a.tracker.Record(Event{
		NodeID:   a.id,
		LeaseID:  a.leaseID,
		Type:     EventHandoff,
		Term:     a.term,
		CausedBy: []uint64{a.lastGrant.Seq},
		Detail:   fmt.Sprintf("lease handed off from %s to %s term=%d", from, to, a.term),
	})
	a.lastGrant = ev
	return true, ev
}

// Release 持有者主动释放租约。
func (a *Arbiter) Release(nodeID string) (bool, Event) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.state != StateHeld || a.holder != nodeID {
		return false, a.tracker.Record(Event{
			NodeID:  a.id,
			LeaseID: a.leaseID,
			Type:    EventRejected,
			Term:    a.term,
			Detail:  fmt.Sprintf("release rejected: %s is not the holder", nodeID),
		})
	}
	a.transitionLocked(StateIdle, a.lastGrant.Seq)
	ev := a.tracker.Record(Event{
		NodeID:   a.id,
		LeaseID:  a.leaseID,
		Type:     EventRelease,
		Term:     a.term,
		CausedBy: []uint64{a.lastGrant.Seq},
		Detail:   fmt.Sprintf("lease released by %s term=%d", nodeID, a.term),
	})
	a.holder = ""
	return true, ev
}

// CheckTimeout 租约清扫：若当前持有者已超时则回收租约。
// 返回是否发生了超时回收。
func (a *Arbiter) CheckTimeout() (bool, Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == StateHeld && !a.now().Before(a.expiry) {
		return true, a.expireLocked()
	}
	return false, Event{}
}

func (a *Arbiter) expireLocked(causes ...uint64) Event {
	if a.lastGrant.Seq != 0 {
		causes = append(causes, a.lastGrant.Seq)
	}
	holder := a.holder
	a.transitionLocked(StateIdle, causes...)
	ev := a.tracker.Record(Event{
		NodeID:   a.id,
		LeaseID:  a.leaseID,
		Type:     EventTimeout,
		Term:     a.term,
		CausedBy: causes,
		Detail:   fmt.Sprintf("lease of %s expired at term=%d; reclaimed by arbiter", holder, a.term),
	})
	a.holder = ""
	return ev
}
