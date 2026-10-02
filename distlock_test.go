package distlock

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 验收场景：A 持锁并心跳 -> A 假死 -> 5 秒租约自然超时 ->
// 排队中的 B 获得锁 -> A 恢复后写操作被 FenceToken 拒绝。
func TestLeaseExpiryAndFencing(t *testing.T) {
	svc := NewLockService()
	nodeA := NewClient(svc, "node-A")
	nodeB := NewClient(svc, "node-B")
	ctx := context.Background()
	const ttl = 5 * time.Second

	// 1. 节点 A 获取锁并启动心跳（Acquire 内部自动启动）。
	lockA, err := nodeA.Acquire(ctx, "resource", ttl)
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if err := lockA.Write("A-1"); err != nil {
		t.Fatalf("A write while holding lock: %v", err)
	}

	// 2. 模拟节点 A 假死挂起：心跳停止。
	lockA.Freeze()

	// 3. 节点 B 排队等待同一把锁。
	type result struct {
		lock    *Lock
		elapsed time.Duration
		err     error
	}
	resCh := make(chan result, 1)
	go func() {
		start := time.Now()
		l, err := nodeB.Acquire(ctx, "resource", ttl)
		resCh <- result{l, time.Since(start), err}
	}()

	// B 在租约到期前不应拿到锁。
	select {
	case r := <-resCh:
		t.Fatalf("B acquired lock too early after %v (lease should still be held by A)", r.elapsed)
	case <-time.After(ttl - 2*time.Second):
	}

	// 4. 心跳停止，租约在 5 秒后自然超时，B 成功获锁。
	r := <-resCh
	if r.err != nil {
		t.Fatalf("B acquire: %v", r.err)
	}
	lockB := r.lock
	if r.elapsed < ttl-time.Second {
		t.Fatalf("B acquired after %v, expected to wait for ~5s lease expiry", r.elapsed)
	}
	t.Logf("node B acquired lock after %v (lease expired naturally)", r.elapsed.Round(time.Millisecond))
	if lockB.Token() <= lockA.Token() {
		t.Fatalf("fence token not monotonic: A=%d B=%d", lockA.Token(), lockB.Token())
	}

	// 5. 节点 A 恢复，尝试写操作 -> 被 FenceToken 拒绝。
	lockA.Unfreeze()
	if err := lockA.Write("A-stale"); !errors.Is(err, ErrFencing) {
		t.Fatalf("expected ErrFencing for stale A write, got %v", err)
	}

	// B 的写正常生效。
	if err := lockB.Write("B-1"); err != nil {
		t.Fatalf("B write: %v", err)
	}
	if got := svc.Read("resource"); got != "B-1" {
		t.Fatalf("resource = %q, want %q (stale A write must not land)", got, "B-1")
	}

	// A 的心跳恢复后应发现锁已丢失。
	deadline := time.Now().Add(3 * time.Second)
	for !lockA.Lost() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !lockA.Lost() {
		t.Fatal("A heartbeat should detect lock loss after recovery")
	}

	lockB.Unlock()
}

// 心跳续期可让锁持有时间远超单个 TTL。
func TestKeepAliveExtendsLease(t *testing.T) {
	svc := NewLockService()
	nodeA := NewClient(svc, "node-A")
	nodeB := NewClient(svc, "node-B")
	ctx := context.Background()
	const ttl = 300 * time.Millisecond

	lockA, err := nodeA.Acquire(ctx, "resource", ttl)
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	defer lockA.Unlock()

	// 持有 1s（> 3 倍 TTL），期间心跳持续续期，锁不释放。
	time.Sleep(time.Second)
	if err := lockA.Write("A-alive"); err != nil {
		t.Fatalf("A write after keepalive: %v", err)
	}

	// B 短时间内拿不到锁。
	bCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := nodeB.Acquire(bCtx, "resource", ttl); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("B should time out while A is alive, got %v", err)
	}
}

// 显式 Unlock 立即唤醒排队等待者，无需等租约超时。
func TestReleaseWakesWaiter(t *testing.T) {
	svc := NewLockService()
	nodeA := NewClient(svc, "node-A")
	nodeB := NewClient(svc, "node-B")
	ctx := context.Background()
	const ttl = 30 * time.Second

	lockA, err := nodeA.Acquire(ctx, "resource", ttl)
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	resCh := make(chan *Lock, 1)
	go func() {
		l, err := nodeB.Acquire(ctx, "resource", ttl)
		if err != nil {
			t.Errorf("B acquire: %v", err)
			return
		}
		resCh <- l
	}()

	time.Sleep(100 * time.Millisecond) // 确保 B 已在排队
	lockA.Unlock()

	select {
	case lockB := <-resCh:
		lockB.Unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("B was not woken up promptly after A released the lock")
	}
}

// FenceToken 全局单调递增。
func TestFenceTokenMonotonic(t *testing.T) {
	svc := NewLockService()
	node := NewClient(svc, "node")
	ctx := context.Background()

	var prev uint64
	for i := 0; i < 3; i++ {
		l, err := node.Acquire(ctx, "resource", time.Second)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if l.Token() <= prev {
			t.Fatalf("token %d not greater than previous %d", l.Token(), prev)
		}
		prev = l.Token()
		l.Unlock()
	}
}

// 租约过期后 KeepAlive 被拒绝。
func TestKeepAliveRejectedAfterExpiry(t *testing.T) {
	svc := NewLockService()
	token, err := svc.Acquire(context.Background(), "resource", "node-A", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := svc.KeepAlive("resource", "node-A", token, time.Second); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expected ErrLeaseExpired, got %v", err)
	}
}
