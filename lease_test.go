package leaseaudit

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// manualClock 可手动推进的假时钟，用于确定性测试超时行为。
type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock() *manualClock {
	return &manualClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func eventsOfType(events []Event, typ EventType) []Event {
	var out []Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// TestRaceContentionEvidence 验收测试：复现两个节点在毫秒级内同时争抢
// 主租约的竞态，验证审计追踪器完整还原到达次序、锁状态变迁、拒绝响应
// 与因果关系证据链。
func TestRaceContentionEvidence(t *testing.T) {
	tracker := NewTracker(nil) // 真实时钟，验证毫秒级到达间隔
	arb := NewArbiter(tracker, "primary", 500*time.Millisecond, 20*time.Millisecond, nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(map[string]bool)
	var resMu sync.Mutex

	for _, node := range []string{"node-A", "node-B"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start // 同一时刻放行，制造毫秒级并发争抢
			ok, _ := arb.Acquire(id)
			resMu.Lock()
			results[id] = ok
			resMu.Unlock()
		}(node)
	}
	close(start)
	wg.Wait()

	// 1) 恰好一个节点获胜，另一个被拒绝
	if results["node-A"] == results["node-B"] {
		t.Fatalf("expected exactly one winner, got %v", results)
	}

	events := tracker.EventsFor("primary")

	// 2) 两份申请都被记录，且到达间隔在毫秒级（竞态窗口内）
	reqs := eventsOfType(events, EventAcquireRequest)
	if len(reqs) != 2 {
		t.Fatalf("expected 2 acquire requests, got %d", len(reqs))
	}
	delta := reqs[1].WallTime.Sub(reqs[0].WallTime)
	if delta < 0 || delta > 20*time.Millisecond {
		t.Fatalf("requests not within race window: delta=%s", delta)
	}
	t.Logf("两份申请到达间隔: %s (seq #%d -> #%d)", delta, reqs[0].Seq, reqs[1].Seq)

	// 3) 向量时钟证明两份申请因果并发 —— 这是竞态的严格判据
	if !Concurrent(reqs[0].Vector, reqs[1].Vector) {
		t.Fatalf("requests should be causally concurrent: %s vs %s",
			reqs[0].Vector, reqs[1].Vector)
	}

	// 4) 锁状态变迁链完整：IDLE -> GRANTING -> HELD
	trans := eventsOfType(events, EventLockTransition)
	if len(trans) != 2 ||
		trans[0].From != StateIdle || trans[0].To != StateGranting ||
		trans[1].From != StateGranting || trans[1].To != StateHeld {
		t.Fatalf("unexpected lock transitions: %+v", trans)
	}

	// 5) 恰好一次授予、一次拒绝、一次冲突
	grants := eventsOfType(events, EventAcquireGranted)
	rejects := eventsOfType(events, EventRejected)
	conflicts := eventsOfType(events, EventConflict)
	if len(grants) != 1 || len(rejects) != 1 || len(conflicts) != 1 {
		t.Fatalf("grants=%d rejects=%d conflicts=%d", len(grants), len(rejects), len(conflicts))
	}

	// 6) 拒绝事件因果上晚于两份申请（向量时钟严格判定）
	rej := rejects[0]
	if !tracker.HappensBefore(reqs[0].Seq, rej.Seq) ||
		!tracker.HappensBefore(reqs[1].Seq, rej.Seq) {
		t.Fatalf("rejection must happen-after both requests")
	}

	// 7) 因果证据链：从拒绝事件回溯，必须覆盖两份申请与锁状态变迁
	chain := tracker.CausalChain(rej.Seq)
	var hasReqA, hasReqB, hasTransition bool
	for _, e := range chain {
		if e.Seq == reqs[0].Seq {
			hasReqA = true
		}
		if e.Seq == reqs[1].Seq {
			hasReqB = true
		}
		if e.Type == EventLockTransition && e.To == StateGranting {
			hasTransition = true
		}
	}
	if !hasReqA || !hasReqB || !hasTransition {
		t.Fatalf("causal chain incomplete: reqA=%v reqB=%v transition=%v\n%s",
			hasReqA, hasReqB, hasTransition, Render(chain))
	}

	// 8) 冲突事件因果上晚于两份申请（它是对竞态的仲裁结论）
	conflict := conflicts[0]
	if !tracker.HappensBefore(reqs[0].Seq, conflict.Seq) ||
		!tracker.HappensBefore(reqs[1].Seq, conflict.Seq) {
		t.Fatalf("conflict must happen-after both requests")
	}

	// 9) 回放顺序与到达次序一致
	replay := Replay(tracker, "primary")
	for i := 1; i < len(replay); i++ {
		if replay[i].Seq <= replay[i-1].Seq {
			t.Fatalf("replay order broken at %d", i)
		}
	}
	t.Logf("回放时间轴:\n%s", Render(replay))
}

// TestRenew 续期：持有者成功，非持有者被拒绝。
func TestRenew(t *testing.T) {
	clock := newManualClock()
	tracker := NewTracker(clock.Now)
	arb := NewArbiter(tracker, "primary", 100*time.Millisecond, 0, clock.Now)

	if ok, _ := arb.Acquire("node-A"); !ok {
		t.Fatal("node-A should win the lease")
	}
	clock.Advance(50 * time.Millisecond)
	if ok, _ := arb.Renew("node-A"); !ok {
		t.Fatal("holder renew should succeed")
	}
	if ok, _ := arb.Renew("node-B"); ok {
		t.Fatal("non-holder renew must be rejected")
	}

	events := tracker.EventsFor("primary")
	if n := len(eventsOfType(events, EventRenewed)); n != 1 {
		t.Fatalf("expected 1 RENEWED, got %d", n)
	}
	rejects := eventsOfType(events, EventRejected)
	if len(rejects) != 1 || !strings.Contains(rejects[0].Detail, "node-B") {
		t.Fatalf("expected rejection of node-B renew, got %+v", rejects)
	}
}

// TestTimeoutAndReacquire 超时回收：到期后仲裁器回收租约，其他节点可重新申请。
func TestTimeoutAndReacquire(t *testing.T) {
	clock := newManualClock()
	tracker := NewTracker(clock.Now)
	arb := NewArbiter(tracker, "primary", 100*time.Millisecond, 0, clock.Now)

	if ok, _ := arb.Acquire("node-A"); !ok {
		t.Fatal("node-A should win the lease")
	}
	clock.Advance(150 * time.Millisecond) // 越过 TTL

	expired, ev := arb.CheckTimeout()
	if !expired || ev.Type != EventTimeout {
		t.Fatalf("expected timeout event, got %+v", ev)
	}
	if state, holder, _ := arb.State(); state != StateIdle || holder != "" {
		t.Fatalf("lease should be reclaimed, state=%s holder=%q", state, holder)
	}

	// 超时后续期应被拒绝
	if ok, _ := arb.Renew("node-A"); ok {
		t.Fatal("renew after timeout must be rejected")
	}
	// 另一节点可重新获得租约，任期递增
	if ok, grant := arb.Acquire("node-B"); !ok || grant.Term != 2 {
		t.Fatalf("node-B should acquire at term 2, got %+v", grant)
	}

	events := tracker.EventsFor("primary")
	if n := len(eventsOfType(events, EventTimeout)); n != 1 {
		t.Fatalf("expected 1 TIMEOUT, got %d", n)
	}
}

// TestHandoff 租约移交：任期递增，旧持有者失去续期权。
func TestHandoff(t *testing.T) {
	clock := newManualClock()
	tracker := NewTracker(clock.Now)
	arb := NewArbiter(tracker, "primary", 100*time.Millisecond, 0, clock.Now)

	if ok, _ := arb.Acquire("node-A"); !ok {
		t.Fatal("node-A should win the lease")
	}
	if ok, ev := arb.Handoff("node-A", "node-B"); !ok || ev.Term != 2 {
		t.Fatalf("handoff should succeed at term 2, got %+v", ev)
	}
	if ok, _ := arb.Renew("node-A"); ok {
		t.Fatal("old holder must lose renew right after handoff")
	}
	if ok, _ := arb.Renew("node-B"); !ok {
		t.Fatal("new holder should renew")
	}
	if ok, _ := arb.Handoff("node-A", "node-C"); ok {
		t.Fatal("handoff from non-holder must be rejected")
	}

	events := tracker.EventsFor("primary")
	if n := len(eventsOfType(events, EventHandoff)); n != 1 {
		t.Fatalf("expected 1 HANDOFF, got %d", n)
	}
}

// TestCausalChainAcrossLifecycle 完整生命周期回放：
// 申请 -> 授予 -> 续期 -> 移交 -> 释放，因果链覆盖全部关键事件。
func TestCausalChainAcrossLifecycle(t *testing.T) {
	clock := newManualClock()
	tracker := NewTracker(clock.Now)
	arb := NewArbiter(tracker, "primary", 100*time.Millisecond, 0, clock.Now)

	arb.Acquire("node-A")
	clock.Advance(10 * time.Millisecond)
	arb.Renew("node-A")
	clock.Advance(10 * time.Millisecond)
	arb.Handoff("node-A", "node-B")
	_, release := arb.Release("node-B")

	chain := tracker.CausalChain(release.Seq)
	var hasGrant, hasHandoff bool
	for _, e := range chain {
		if e.Type == EventAcquireGranted {
			hasGrant = true
		}
		if e.Type == EventHandoff {
			hasHandoff = true
		}
	}
	if !hasGrant || !hasHandoff {
		t.Fatalf("release chain should include grant and handoff:\n%s", Render(chain))
	}

	replay := Replay(tracker, "primary")
	t.Logf("生命周期回放:\n%s", Render(replay))
}

// TestVectorClock 向量时钟单元测试。
func TestVectorClock(t *testing.T) {
	a := VectorClock{"n1": 1}
	b := VectorClock{"n1": 1, "n2": 1}
	c := VectorClock{"n2": 1}

	if !HappensBefore(a, b) {
		t.Fatal("a should happen-before b")
	}
	if HappensBefore(b, a) {
		t.Fatal("b should not happen-before a")
	}
	if !Concurrent(a, c) || !Concurrent(c, a) {
		t.Fatal("a and c should be concurrent")
	}
	if HappensBefore(a, a) {
		t.Fatal("a should not happen-before itself")
	}
}
