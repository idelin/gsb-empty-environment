package lease

import (
	"testing"
	"time"
)

const (
	testTTL     = 300 * time.Millisecond
	waitTimeout = 3 * time.Second
)

// waitFor 轮询直到条件满足或超时。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

func countLeaders(c *Cluster) (int, []NodeID) {
	n := 0
	var ids []NodeID
	for _, node := range c.Nodes() {
		if node.IsLeader() {
			n++
			ids = append(ids, node.ID())
		}
	}
	return n, ids
}

// TestElectLeaderRequiresQuorum 验证：获得多数派授权才能成为 Leader，
// 且租约未过期时其他候选者无法夺取授权。
func TestElectLeaderRequiresQuorum(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()

	if !c.Node(0).Campaign(testTTL) {
		t.Fatal("node-0 should win the election with 5 reachable nodes")
	}
	if !c.Node(0).IsLeader() {
		t.Fatal("node-0 should be leader")
	}
	if got := c.Quorum(); got != 3 {
		t.Fatalf("quorum of 5-node cluster = %d, want 3", got)
	}

	// 租约仍被 node-0 持有，node-1 竞选应失败。
	if c.Node(1).Campaign(testTTL) {
		t.Fatal("node-1 must not become leader while node-0 holds the lease")
	}
	if n, _ := countLeaders(c); n != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", n)
	}
}

// TestMinorityPartitionCannotElect 验证：少数派分区（2/5）无法凑齐 Quorum，竞选失败。
func TestMinorityPartitionCannotElect(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()

	c.Partition(0, 1) // {0,1} | {2,3,4}

	if c.Node(0).Campaign(testTTL) {
		t.Fatal("node-0 in minority partition must not win election")
	}
	if !c.Node(2).Campaign(testTTL) {
		t.Fatal("node-2 in majority partition should win election")
	}
}

// TestSplitBrainPrevention 验收边界：
// 5 节点集群划分为 2 节点和 3 节点分区后，
// 3 节点分区成功续租并继续服务；2 节点分区租约过期后自动剥离主权限。
func TestSplitBrainPrevention(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()

	// 1. 全集群选举 node-0 为 Leader。
	if !c.Node(0).Campaign(testTTL) {
		t.Fatal("node-0 should win initial election")
	}
	if err := c.Node(0).Write("k", "v"); err != nil {
		t.Fatalf("leader should accept writes, got %v", err)
	}

	// 2. 对称分区：{0,1} | {2,3,4}，旧 Leader 位于少数派。
	c.Partition(0, 1)

	// 3. 多数派分区：等待 node-0 的授权过期后，node-3 赢得选举。
	waitFor(t, "node-3 becomes leader in majority partition", func() bool {
		return c.Node(3).Campaign(testTTL)
	})

	// 4. 少数派旧 Leader 租约过期后自动退位。
	waitFor(t, "node-0 loses leadership after lease expiry", func() bool {
		return !c.Node(0).IsLeader()
	})
	if err := c.Node(0).Write("k", "v2"); err != ErrNotLeader {
		t.Fatalf("minority node must reject writes, got %v", err)
	}

	// 5. 多数派 Leader 持续续租并保持服务（跨越多个 TTL 周期）。
	time.Sleep(2 * testTTL)
	if !c.Node(3).IsLeader() {
		t.Fatal("majority leader should keep renewing its lease")
	}
	if err := c.Node(3).Write("k", "v3"); err != nil {
		t.Fatalf("majority leader should accept writes, got %v", err)
	}

	// 6. 任意时刻全集群至多一个有效 Leader。
	if n, ids := countLeaders(c); n != 1 {
		t.Fatalf("split brain detected: %d leaders %v", n, ids)
	}

	// 7. 分区恢复后，旧 Leader 不复活，仍拒绝写。
	c.Heal()
	time.Sleep(testTTL / 2)
	if c.Node(0).IsLeader() {
		t.Fatal("node-0 must not regain leadership after heal")
	}
	if err := c.Node(0).Write("k", "v4"); err != ErrNotLeader {
		t.Fatalf("healed minority node must still reject writes, got %v", err)
	}
	if !c.Node(3).IsLeader() {
		t.Fatal("node-3 should remain leader after heal")
	}
}

// TestAsymmetricPartition 验证：非对称分区下少数派 Leader 同样丧失租约。
// 仅断开 node-0 -> {2,3,4} 方向（node-0 仍可收消息），其续租请求无法送达多数派。
func TestAsymmetricPartition(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()

	if !c.Node(0).Campaign(testTTL) {
		t.Fatal("node-0 should win initial election")
	}

	for _, id := range []NodeID{2, 3, 4} {
		c.BlockLink(0, id)
	}

	// node-0 续租只能拿到 {0,1} 两票，租约过期后自动退位。
	waitFor(t, "node-0 steps down under asymmetric partition", func() bool {
		return !c.Node(0).IsLeader()
	})
	if err := c.Node(0).Write("k", "v"); err != ErrNotLeader {
		t.Fatalf("asymmetrically partitioned leader must reject writes, got %v", err)
	}

	// 多数派一侧内部通信完好，可正常选主。
	waitFor(t, "node-4 becomes leader", func() bool {
		return c.Node(4).Campaign(testTTL)
	})
	if n, _ := countLeaders(c); n != 1 {
		t.Fatal("exactly one leader expected")
	}
}

// TestLeaseExpiryAutoRevoke 验证：无任何竞选时，Leader 失联后租约到期自动退位。
func TestLeaseExpiryAutoRevoke(t *testing.T) {
	c := NewCluster(3)
	defer c.Shutdown()

	if !c.Node(0).Campaign(testTTL) {
		t.Fatal("node-0 should win election")
	}
	c.Partition(0) // {0} | {1,2}

	waitFor(t, "isolated leader lease auto-revoked", func() bool {
		return !c.Node(0).IsLeader()
	})
	if err := c.Node(0).Write("k", "v"); err != ErrNotLeader {
		t.Fatalf("expired leader must reject writes, got %v", err)
	}
}
