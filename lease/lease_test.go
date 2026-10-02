package lease

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestAcquireAndDynamicRenew 验证获取租约后动态续期能长期保活,且到期时间持续后移。
func TestAcquireAndDynamicRenew(t *testing.T) {
	c := NewCoordinator()
	a := NewNode("A", c)
	defer a.Shutdown()

	if err := a.Acquire("svc", 40*time.Millisecond, map[string]string{"shard": "0"}); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	first, _ := a.Lease()
	time.Sleep(150 * time.Millisecond) // 远超单个 Term,靠动态续期保活
	if !a.IsPrimary() {
		t.Fatal("租约应通过动态续期保持有效")
	}
	cur, _ := a.Lease()
	if !cur.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("续期应推后到期时间: first=%v cur=%v", first.ExpiresAt, cur.ExpiresAt)
	}
	if got, _ := c.Primary("svc"); got != "A" {
		t.Fatalf("Primary = %q, want A", got)
	}
}

// TestGracefulHandover 验收边界:A 收到滚动重启指令,冻结写入并向 B 移交,
// B 无缝继承剩余租约上下文,切换耗时仅毫秒级。
func TestGracefulHandover(t *testing.T) {
	c := NewCoordinator()
	a := NewNode("A", c)
	b := NewNode("B", c)
	defer a.Shutdown()
	defer b.Shutdown()

	ctx := map[string]string{"shard": "7", "offset": "1024"}
	if err := a.Acquire("svc", 500*time.Millisecond, ctx); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	before, _ := a.Lease()

	d, err := a.GracefulHandover("B")
	if err != nil {
		t.Fatalf("GracefulHandover: %v", err)
	}
	t.Logf("业务切换耗时: %v", d)
	if d > 50*time.Millisecond {
		t.Fatalf("移交耗时 %v 超过 50ms 上限", d)
	}

	if a.IsPrimary() {
		t.Fatal("A 移交后不应再是主节点")
	}
	if !b.IsPrimary() {
		t.Fatal("B 应立即接管主角色")
	}
	got, ok := b.Lease()
	if !ok {
		t.Fatal("B 应持有租约凭证")
	}
	if got.FencingToken <= before.FencingToken {
		t.Fatalf("fencing token 应递增: before=%d got=%d", before.FencingToken, got.FencingToken)
	}
	if !got.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("B 应继承剩余 TTL: want %v got %v", before.ExpiresAt, got.ExpiresAt)
	}
	if got.Context["shard"] != "7" || got.Context["offset"] != "1024" {
		t.Fatalf("B 应继承业务上下文, got %v", got.Context)
	}
	if id, _ := c.Primary("svc"); id != "B" {
		t.Fatalf("Primary = %q, want B", id)
	}
	// B 接管后续期循环继续工作。
	time.Sleep(100 * time.Millisecond)
	if !b.IsPrimary() {
		t.Fatal("B 接管后应通过续期保持主角色")
	}
}

// TestZeroErrorSwitchover 验证移交全过程业务写入零错误中断。
func TestZeroErrorSwitchover(t *testing.T) {
	c := NewCoordinator()
	a := NewNode("A", c)
	b := NewNode("B", c)
	defer a.Shutdown()
	defer b.Shutdown()

	if err := a.Acquire("svc", 300*time.Millisecond, nil); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	var writes, errs int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := clientWrite(c, "svc", "event"); err != nil {
				atomic.AddInt64(&errs, 1)
			} else {
				atomic.AddInt64(&writes, 1)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond) // 稳定跑一段时间
	if _, err := a.GracefulHandover("B"); err != nil {
		t.Fatalf("GracefulHandover: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	<-done

	t.Logf("写入成功 %d 次, 错误 %d 次", atomic.LoadInt64(&writes), atomic.LoadInt64(&errs))
	if atomic.LoadInt64(&errs) != 0 {
		t.Fatalf("移交过程出现 %d 次写入错误,期望零错误", errs)
	}
	if atomic.LoadInt64(&writes) == 0 {
		t.Fatal("应有成功写入")
	}
}

// clientWrite 模拟业务客户端:解析当前主节点并写入,冻结窗口内短暂重试。
func clientWrite(c *Coordinator, leaseID, payload string) error {
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		id, err := c.Primary(leaseID)
		if err == nil {
			if n := c.Node(id); n != nil {
				if werr := n.Write(payload); werr == nil {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return ErrHandoverFailed
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStaleHolderRejected 验证移交后旧持有者的写入与续期被 fencing token 拒绝。
func TestStaleHolderRejected(t *testing.T) {
	c := NewCoordinator()
	a := NewNode("A", c)
	b := NewNode("B", c)
	defer a.Shutdown()
	defer b.Shutdown()

	if err := a.Acquire("svc", 500*time.Millisecond, nil); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	old, _ := a.Lease()
	if _, err := a.GracefulHandover("B"); err != nil {
		t.Fatalf("GracefulHandover: %v", err)
	}

	if err := a.Write("x"); err != ErrNotPrimary {
		t.Fatalf("旧持有者写入应返回 ErrNotPrimary, got %v", err)
	}
	if err := c.ValidateWrite("svc", "A", old.FencingToken); err != ErrNotHolder {
		t.Fatalf("旧持有者 fencing 校验应返回 ErrNotHolder, got %v", err)
	}
	if _, err := c.Renew("svc", "A", old.FencingToken); err != ErrNotHolder {
		t.Fatalf("旧持有者续期应返回 ErrNotHolder, got %v", err)
	}
	cur, _ := b.Lease()
	if err := c.ValidateWrite("svc", "B", cur.FencingToken); err != nil {
		t.Fatalf("新持有者写入应合法, got %v", err)
	}
}

// TestHandoverToUnknownCandidate 验证候选人不存在时移交失败且原持有者解冻继续服务。
func TestHandoverToUnknownCandidate(t *testing.T) {
	c := NewCoordinator()
	a := NewNode("A", c)
	defer a.Shutdown()

	if err := a.Acquire("svc", 500*time.Millisecond, nil); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := a.GracefulHandover("ghost"); err != ErrNoCandidate {
		t.Fatalf("应返回 ErrNoCandidate, got %v", err)
	}
	if !a.IsPrimary() {
		t.Fatal("移交失败后 A 应继续作为主节点")
	}
	if err := a.Write("x"); err != nil {
		t.Fatalf("移交失败后 A 应解冻并可写, got %v", err)
	}
}

// TestNaturalExpiry 验证无人续期时租约自然过期,其他节点可重新获取。
func TestNaturalExpiry(t *testing.T) {
	c := NewCoordinator()
	a := NewNode("A", c)
	b := NewNode("B", c)
	defer a.Shutdown()
	defer b.Shutdown()

	if err := a.Acquire("svc", 30*time.Millisecond, nil); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	a.Shutdown() // 直接宕机,未移交
	time.Sleep(60 * time.Millisecond)
	if err := b.Acquire("svc", 30*time.Millisecond, nil); err != nil {
		t.Fatalf("自然过期后 B 应能获取租约, got %v", err)
	}
}
