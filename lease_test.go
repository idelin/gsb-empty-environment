package lease

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock 可手动推进的时钟，避免测试中真实睡眠。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

func newTestCluster(ttl time.Duration) (*Center, *fakeClock) {
	clock := newFakeClock()
	c := newCenter(ttl, map[string]string{"db_password": "old-secret"}, clock.Now)
	return c, clock
}

func newTestNode(name string, c *Center, clock *fakeClock) *Node {
	n := NewNode(name, c)
	n.now = clock.Now
	return n
}

func mustServe(t *testing.T, n *Node) string {
	t.Helper()
	resp, err := n.Serve("query")
	if err != nil {
		t.Fatalf("%s should serve, got err: %v", n.Name(), err)
	}
	return resp
}

func mustNotServe(t *testing.T, n *Node) {
	t.Helper()
	if _, err := n.Serve("query"); err != ErrNoValidLease {
		t.Fatalf("%s should refuse to serve, got err: %v", n.Name(), err)
	}
}

// 场景一：配置中心修改数据库密码，发布新配置并收回旧租约。
func TestUpdatePublishesNewConfigAndRevokesOldLeases(t *testing.T) {
	c, clock := newTestCluster(time.Minute)
	n1 := newTestNode("node-1", c, clock)
	n2 := newTestNode("node-2", c, clock)
	if err := n1.Start(); err != nil {
		t.Fatal(err)
	}
	if err := n2.Start(); err != nil {
		t.Fatal(err)
	}

	// 初始：两节点持 v1 配置（旧密码）对外服务。
	if resp := mustServe(t, n1); !strings.Contains(resp, "configVersion=1 dbPassword=old-secret") {
		t.Fatalf("unexpected response: %s", resp)
	}
	mustServe(t, n2)

	// 配置中心修改数据库密码 -> 版本升级，旧租约全部撤销。
	c.Update(map[string]string{"db_password": "new-secret"})
	if got := c.CurrentConfig().Version; got != 2 {
		t.Fatalf("config version = %d, want 2", got)
	}

	// 旧租约在配置中心侧已失效：续期被拒绝，节点立即停止服务。
	if err := n1.Renew(); err != ErrLeaseRevoked {
		t.Fatalf("renew with revoked lease: got %v, want ErrLeaseRevoked", err)
	}
	mustNotServe(t, n1)

	// 配置中心向所有节点发布新配置与新租约凭证。
	if err := c.Publish(n1); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(n2); err != nil {
		t.Fatal(err)
	}

	// 两节点持 v2 配置（新密码）恢复服务。
	for _, n := range []*Node{n1, n2} {
		if resp := mustServe(t, n); !strings.Contains(resp, "configVersion=2 dbPassword=new-secret") {
			t.Fatalf("unexpected response: %s", resp)
		}
	}
}

// 场景二：脱网慢节点收不到推送，旧租约自然失效后主动停止接收流量。
func TestOfflineNodeStopsServingAfterLeaseExpiry(t *testing.T) {
	ttl := 30 * time.Second
	c, clock := newTestCluster(ttl)
	fast := newTestNode("fast-node", c, clock)
	slow := newTestNode("slow-node", c, clock)
	mustStart := func(n *Node) {
		if err := n.Start(); err != nil {
			t.Fatal(err)
		}
	}
	mustStart(fast)
	mustStart(slow)

	// 慢节点脱网。
	slow.SetOnline(false)

	// 配置中心修改密码并发布：快节点收到 v2，慢节点推送失败。
	c.Update(map[string]string{"db_password": "new-secret"})
	if err := c.Publish(fast); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(slow); err != ErrNodeOffline {
		t.Fatalf("publish to offline node: got %v, want ErrNodeOffline", err)
	}

	// 租约到期前：慢节点仍持旧配置短暂服务（窗口期由 TTL 约束）。
	if resp := mustServe(t, slow); !strings.Contains(resp, "configVersion=1 dbPassword=old-secret") {
		t.Fatalf("unexpected response: %s", resp)
	}

	// 快节点在到期前续期，服务不中断。
	clock.Advance(20 * time.Second)
	if err := fast.Renew(); err != nil {
		t.Fatal(err)
	}

	// 旧租约自然过期：慢节点主动停止接收流量，避免用旧密码操作脏数据。
	clock.Advance(ttl - 20*time.Second + time.Second)
	mustNotServe(t, slow)
	if status := slow.Status(); !strings.Contains(status, "NOT SERVING") {
		t.Fatalf("unexpected status: %s", status)
	}

	// 快节点已续期，全程正常服务。
	if resp := mustServe(t, fast); !strings.Contains(resp, "configVersion=2 dbPassword=new-secret") {
		t.Fatalf("unexpected response: %s", resp)
	}

	// 慢节点恢复联网后重新租用，拿到新配置才恢复服务。
	slow.SetOnline(true)
	if err := slow.Start(); err != nil {
		t.Fatal(err)
	}
	if resp := mustServe(t, slow); !strings.Contains(resp, "configVersion=2 dbPassword=new-secret") {
		t.Fatalf("unexpected response: %s", resp)
	}
}

// 版本约束：绑定旧版本的租约在配置升级后一律失效。
func TestVersionConstraint(t *testing.T) {
	c, _ := newTestCluster(time.Minute)
	_, old := c.Issue()
	if err := c.Validate(old.Token); err != nil {
		t.Fatal(err)
	}

	c.Update(map[string]string{"db_password": "rotated"})

	if err := c.Validate(old.Token); err != ErrLeaseRevoked {
		t.Fatalf("validate old token: got %v, want ErrLeaseRevoked", err)
	}
	if _, _, err := c.Renew(old.Token); err != ErrLeaseRevoked {
		t.Fatalf("renew old token: got %v, want ErrLeaseRevoked", err)
	}
	if _, _, err := c.Renew("no-such-token"); err != ErrUnknownToken {
		t.Fatalf("renew unknown token: got %v, want ErrUnknownToken", err)
	}
}

// 续期：到期前续期可延长服务；过期后续期被拒绝。
func TestRenewLifecycle(t *testing.T) {
	ttl := 30 * time.Second
	c, clock := newTestCluster(ttl)
	n := newTestNode("node", c, clock)
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}

	clock.Advance(20 * time.Second)
	if err := n.Renew(); err != nil {
		t.Fatal(err)
	}
	// 越过原过期时间，因已续期仍可服务。
	clock.Advance(20 * time.Second)
	mustServe(t, n)

	// 不再续期，租约过期后停止服务，且过期租约无法续命。
	clock.Advance(ttl + time.Second)
	mustNotServe(t, n)
	if err := n.Renew(); err != ErrLeaseExpired {
		t.Fatalf("renew after expiry: got %v, want ErrLeaseExpired", err)
	}
}

// 并发安全：多节点并发服务/续期，配置中心并发更新与发布。
func TestConcurrentAccess(t *testing.T) {
	c, _ := newTestCluster(time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		n := NewNode("node", c)
		if err := n.Start(); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = n.Serve("q")
				_ = n.Renew()
			}
		}(n)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 5; j++ {
			c.Update(map[string]string{"db_password": "rotated"})
		}
	}()
	wg.Wait()
}
