package leasepool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBasicAcquireAndReturn(t *testing.T) {
	p := NewPool(100, 10*time.Millisecond)
	defer p.Close()

	lease, err := p.Acquire(context.Background(), "biz-A", 60, time.Minute, PriorityNormal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := p.Available(); got != 40 {
		t.Fatalf("available = %d, want 40", got)
	}
	if err := p.Return(lease); err != nil {
		t.Fatalf("return: %v", err)
	}
	if got := p.Available(); got != 100 {
		t.Fatalf("available = %d, want 100", got)
	}
	if err := p.Return(lease); err == nil {
		t.Fatal("double return should fail")
	}
}

func TestAcquireExceedsTotal(t *testing.T) {
	p := NewPool(100, 10*time.Millisecond)
	defer p.Close()
	if _, err := p.Acquire(context.Background(), "biz-A", 101, time.Minute, PriorityNormal); err != ErrExceedsTotal {
		t.Fatalf("err = %v, want ErrExceedsTotal", err)
	}
}

func TestBlockingAcquireUntilReturn(t *testing.T) {
	p := NewPool(100, 10*time.Millisecond)
	defer p.Close()

	first, err := p.Acquire(context.Background(), "biz-A", 100, time.Minute, PriorityNormal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	granted := make(chan struct{})
	go func() {
		defer close(granted)
		if _, err := p.Acquire(context.Background(), "biz-B", 50, time.Minute, PriorityNormal); err != nil {
			t.Errorf("acquire: %v", err)
		}
	}()

	select {
	case <-granted:
		t.Fatal("acquire should block while pool is exhausted")
	case <-time.After(100 * time.Millisecond):
	}

	if err := p.Return(first); err != nil {
		t.Fatalf("return: %v", err)
	}
	select {
	case <-granted:
	case <-time.After(2 * time.Second):
		t.Fatal("acquire should be granted after return")
	}
	if got := p.Available(); got != 50 {
		t.Fatalf("available = %d, want 50", got)
	}
}

// TestAcceptanceRevokeOverdueAndPreempt 验收场景：
// 业务 A 租赁 80 个配额，到期未归还；高优业务 B 申请 50 个配额；
// 协调器强行吊销 A 的超期租约，将配额转借给 B，并输出违约惩罚审计日志。
func TestAcceptanceRevokeOverdueAndPreempt(t *testing.T) {
	p := NewPool(100, 20*time.Millisecond)
	defer p.Close()

	leaseA, err := p.Acquire(context.Background(), "business-A", 80, 200*time.Millisecond, PriorityNormal)
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if got := p.Available(); got != 20 {
		t.Fatalf("available = %d, want 20", got)
	}

	// 等待 A 的租约超期（A 始终不归还）。
	time.Sleep(400 * time.Millisecond)

	// 高优业务 B 申请 50 个配额：空闲仅 20，必须抢占 A 的超期租约。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	leaseB, err := p.Acquire(ctx, "business-B", 50, time.Minute, PriorityHigh)
	if err != nil {
		t.Fatalf("B acquire: %v", err)
	}
	if leaseB.Count != 50 {
		t.Fatalf("B count = %d, want 50", leaseB.Count)
	}

	// A 的租约已被强制吊销，配额被回收转借。
	if !leaseA.Revoked() {
		t.Fatal("A's lease should be revoked")
	}
	if got := p.Available(); got != 50 {
		t.Fatalf("available = %d, want 50 (100 - 50 granted to B)", got)
	}

	// 违约惩罚：A 累计 80 分。
	if got := p.Penalty("business-A"); got != 80 {
		t.Fatalf("A penalty = %d, want 80", got)
	}
	if got := p.Penalty("business-B"); got != 0 {
		t.Fatalf("B penalty = %d, want 0", got)
	}

	// 审计日志必须包含 GRANT(A) / REVOKE(A) / PENALTY(A) / GRANT(B)。
	var grantA, revokeA, penaltyA, grantB bool
	for _, e := range p.AuditLog() {
		t.Logf("audit: %s", e)
		switch {
		case e.Type == EventGrant && e.Owner == "business-A" && e.Count == 80:
			grantA = true
		case e.Type == EventRevoke && e.Owner == "business-A" && e.Count == 80:
			revokeA = true
			if !strings.Contains(e.Detail, "overdue") {
				t.Errorf("revoke detail should mention overdue: %q", e.Detail)
			}
		case e.Type == EventPenalty && e.Owner == "business-A":
			penaltyA = true
		case e.Type == EventGrant && e.Owner == "business-B" && e.Count == 50:
			grantB = true
		}
	}
	if !grantA || !revokeA || !penaltyA || !grantB {
		t.Fatalf("audit incomplete: grantA=%v revokeA=%v penaltyA=%v grantB=%v",
			grantA, revokeA, penaltyA, grantB)
	}

	// 被吊销的租约再归还应当失败。
	if err := p.Return(leaseA); err == nil {
		t.Fatal("returning a revoked lease should fail")
	}
}

// TestSweeperRevokesOverdue 协调器巡检协程主动回收超期租约（无需新申请触发）。
func TestSweeperRevokesOverdue(t *testing.T) {
	p := NewPool(100, 20*time.Millisecond)
	defer p.Close()

	lease, err := p.Acquire(context.Background(), "biz-A", 70, 100*time.Millisecond, PriorityNormal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.Available() == 100 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := p.Available(); got != 100 {
		t.Fatalf("available = %d, want 100 after sweep", got)
	}
	if !lease.Revoked() {
		t.Fatal("lease should be revoked by sweeper")
	}
	if got := p.Penalty("biz-A"); got != 70 {
		t.Fatalf("penalty = %d, want 70", got)
	}
}

// TestPriorityPreemption 高优先级等待者插队：配额释放后优先满足高优请求。
func TestPriorityPreemption(t *testing.T) {
	p := NewPool(100, 10*time.Millisecond)
	defer p.Close()

	holder1, err := p.Acquire(context.Background(), "biz-H", 50, time.Minute, PriorityNormal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	holder2, err := p.Acquire(context.Background(), "biz-H", 50, time.Minute, PriorityNormal)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	lowGranted := make(chan struct{})
	go func() {
		defer close(lowGranted)
		if _, err := p.Acquire(context.Background(), "biz-low", 50, time.Minute, PriorityLow); err != nil {
			t.Errorf("low acquire: %v", err)
		}
	}()
	// 确保低优请求先进入等待队列。
	time.Sleep(100 * time.Millisecond)

	highGranted := make(chan struct{})
	go func() {
		defer close(highGranted)
		if _, err := p.Acquire(context.Background(), "biz-high", 50, time.Minute, PriorityHigh); err != nil {
			t.Errorf("high acquire: %v", err)
		}
	}()
	// 确保高优请求也进入等待队列。
	time.Sleep(100 * time.Millisecond)

	// 伪造租约归还应被拒绝。
	if err := p.Return(&Lease{ID: holder1.ID, Owner: holder1.Owner, Count: 50}); err == nil {
		t.Fatal("returning a forged lease should fail")
	}

	// 只释放 50 个配额：应被高优请求插队获得，低优请求继续阻塞。
	if err := p.Return(holder1); err != nil {
		t.Fatalf("return: %v", err)
	}
	select {
	case <-highGranted:
	case <-time.After(2 * time.Second):
		t.Fatal("high-priority acquire should be granted first")
	}
	select {
	case <-lowGranted:
		t.Fatal("low-priority acquire should still be blocked")
	case <-time.After(150 * time.Millisecond):
	}

	// 再释放 50 个配额：低优请求终于被满足。
	if err := p.Return(holder2); err != nil {
		t.Fatalf("return: %v", err)
	}
	select {
	case <-lowGranted:
	case <-time.After(2 * time.Second):
		t.Fatal("low-priority acquire should be granted after quota freed")
	}
}

func TestAcquireContextTimeout(t *testing.T) {
	p := NewPool(100, 10*time.Millisecond)
	defer p.Close()

	if _, err := p.Acquire(context.Background(), "biz-A", 100, time.Minute, PriorityNormal); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Acquire(ctx, "biz-B", 50, time.Minute, PriorityNormal); err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
