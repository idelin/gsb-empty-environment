package lease

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func eventsOfType(audit []AuditEvent, t AuditEventType) []AuditEvent {
	var out []AuditEvent
	for _, e := range audit {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// 验收边界：业务 A 租赁 80 个配额，到期未归还；高优业务 B 申请 50 个配额；
// 协调器强行吊销 A 的超期租约，配额转借给 B，并输出违约惩罚审计日志。
func TestAcceptance_ExpiredLeaseRevokedAndRelentToHighPriority(t *testing.T) {
	pool := NewPool(100)
	coord := NewCoordinator(pool, 10*time.Millisecond)
	coord.Start()
	defer coord.Stop()

	// 业务 A 租赁 80 个配额，承诺 100ms 后归还（测试中将 10s 等比缩小）。
	leaseA, err := pool.Grant("tenant-A", 80, 100*time.Millisecond, PriorityNormal)
	if err != nil {
		t.Fatalf("grant A: %v", err)
	}
	if got := pool.Available(); got != 20 {
		t.Fatalf("available after A = %d, want 20", got)
	}

	// A 到期未归还，等待协调器强制回收。
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap, _ := pool.Lease(leaseA.ID)
		if snap.Revoked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("coordinator did not revoke expired lease A in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := pool.Available(); got != 100 {
		t.Fatalf("available after revoke = %d, want 100", got)
	}

	// 高优业务 B 申请 50 个配额，配额转借成功。
	leaseB, err := pool.Grant("tenant-B", 50, time.Second, PriorityHigh)
	if err != nil {
		t.Fatalf("grant B: %v", err)
	}
	if leaseB.Count != 50 {
		t.Fatalf("B count = %d, want 50", leaseB.Count)
	}
	if got := pool.Available(); got != 50 {
		t.Fatalf("available after B = %d, want 50", got)
	}

	// 审计日志：A 被强制吊销 + 违约惩罚，B 正常发放。
	audit := pool.Audit()
	revokes := eventsOfType(audit, EventRevoke)
	if len(revokes) != 1 || revokes[0].LeaseID != leaseA.ID || revokes[0].Tenant != "tenant-A" {
		t.Fatalf("expected exactly one REVOKE_EXPIRED for A, got %+v", revokes)
	}
	penalties := eventsOfType(audit, EventPenalty)
	if len(penalties) != 1 || penalties[0].LeaseID != leaseA.ID {
		t.Fatalf("expected exactly one PENALTY for A, got %+v", penalties)
	}
	grants := eventsOfType(audit, EventGrant)
	if len(grants) != 2 || grants[1].LeaseID != leaseB.ID {
		t.Fatalf("expected GRANT for A and B, got %+v", grants)
	}
}

// 高优插队抢占：配额不足且无超期租约时，高优请求抢占低优在租租约。
func TestPreemption_HighPriorityPreemptsLow(t *testing.T) {
	pool := NewPool(100)

	leaseA, err := pool.Grant("tenant-A", 80, time.Hour, PriorityLow)
	if err != nil {
		t.Fatalf("grant A: %v", err)
	}
	if _, err := pool.Grant("tenant-B", 50, time.Hour, PriorityHigh); err != nil {
		t.Fatalf("grant B should preempt A: %v", err)
	}

	snap, _ := pool.Lease(leaseA.ID)
	if !snap.Revoked {
		t.Fatal("low-priority lease A should be preempted")
	}
	if got := pool.Available(); got != 50 {
		t.Fatalf("available = %d, want 50", got)
	}
	if n := len(eventsOfType(pool.Audit(), EventPreempt)); n != 1 {
		t.Fatalf("expected 1 PREEMPT event, got %d", n)
	}
}

// 部分抢占：只抢占足够数量的低优租约。
func TestPreemption_Partial(t *testing.T) {
	pool := NewPool(100)
	l1, _ := pool.Grant("low-1", 30, time.Hour, PriorityLow)
	l2, _ := pool.Grant("low-2", 30, time.Hour, PriorityLow)
	if _, err := pool.Grant("boss", 50, time.Hour, PriorityHigh); err != nil {
		t.Fatalf("grant boss: %v", err)
	}
	s1, _ := pool.Lease(l1.ID)
	s2, _ := pool.Lease(l2.ID)
	// 可用 40，抢占一个 30 的租约后即凑齐 50，另一个不受影响。
	if s1.Revoked == s2.Revoked {
		t.Fatalf("exactly one low lease should be preempted, got l1.Revoked=%v l2.Revoked=%v", s1.Revoked, s2.Revoked)
	}
	if got := pool.Available(); got != 20 {
		t.Fatalf("available = %d, want 20", got)
	}
}

// 同优先级不可抢占，配额不足时报错。
func TestGrant_InsufficientQuotaSamePriority(t *testing.T) {
	pool := NewPool(100)
	if _, err := pool.Grant("tenant-A", 80, time.Hour, PriorityNormal); err != nil {
		t.Fatalf("grant A: %v", err)
	}
	if _, err := pool.Grant("tenant-B", 50, time.Hour, PriorityNormal); err != ErrInsufficientQuota {
		t.Fatalf("err = %v, want ErrInsufficientQuota", err)
	}
}

// 主动归还（还款）释放配额。
func TestReturn_ReleasesQuota(t *testing.T) {
	pool := NewPool(100)
	l, err := pool.Grant("tenant-A", 60, time.Hour, PriorityNormal)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := pool.Return(l.ID); err != nil {
		t.Fatalf("return: %v", err)
	}
	if got := pool.Available(); got != 100 {
		t.Fatalf("available = %d, want 100", got)
	}
	if err := pool.Return(l.ID); err != ErrLeaseClosed {
		t.Fatalf("double return err = %v, want ErrLeaseClosed", err)
	}
	if err := pool.Return("L9999"); err != ErrLeaseNotFound {
		t.Fatalf("unknown lease err = %v, want ErrLeaseNotFound", err)
	}
}

// 逾期归还同样触发违约惩罚审计。
func TestReturn_LateReturnPenalty(t *testing.T) {
	pool := NewPool(100)
	l, _ := pool.Grant("tenant-A", 10, 20*time.Millisecond, PriorityNormal)
	time.Sleep(40 * time.Millisecond)
	if err := pool.Return(l.ID); err != nil {
		t.Fatalf("return: %v", err)
	}
	if n := len(eventsOfType(pool.Audit(), EventPenalty)); n != 1 {
		t.Fatalf("expected 1 PENALTY for late return, got %d", n)
	}
}

// 非法租赁数量。
func TestGrant_InvalidCount(t *testing.T) {
	pool := NewPool(100)
	if _, err := pool.Grant("x", 0, time.Second, PriorityNormal); err != ErrInvalidCount {
		t.Fatalf("err = %v, want ErrInvalidCount", err)
	}
	if _, err := pool.Grant("x", 101, time.Second, PriorityNormal); err != ErrInvalidCount {
		t.Fatalf("err = %v, want ErrInvalidCount", err)
	}
}

// 并发压力：多业务线并发借贷还款，配额占用永不超过总量，最终全部归还。
func TestConcurrent_GrantReturnNeverExceedsQuota(t *testing.T) {
	pool := NewPool(100)
	coord := NewCoordinator(pool, 5*time.Millisecond)
	coord.Start()
	defer coord.Stop()

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tenant := fmt.Sprintf("tenant-%02d", id)
			for i := 0; i < 50; i++ {
				l, err := pool.Grant(tenant, 5, 10*time.Millisecond, Priority(id%3))
				if err == nil {
					if got := pool.Available(); got < 0 || got > 100 {
						t.Errorf("available out of range: %d", got)
					}
					_ = pool.Return(l.ID)
				} else if err != ErrInsufficientQuota {
					t.Errorf("unexpected err: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	pool.RevokeExpired()
	time.Sleep(20 * time.Millisecond)
	pool.RevokeExpired()
	if got := pool.Available(); got != 100 {
		t.Fatalf("available at end = %d, want 100", got)
	}
}
