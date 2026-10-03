package lease

import (
	"errors"
	"testing"
	"time"
)

const testLease = 10 * time.Second

func newTestCluster(size int) (*Cluster, *FakeClock) {
	clk := NewFakeClock(time.Unix(1_700_000_000, 0))
	return NewCluster(size, testLease, clk), clk
}

// 候选者获得多数派授权后成为 Leader，可接受写请求。
func TestMajorityElection(t *testing.T) {
	c, _ := newTestCluster(5)
	n1 := c.Node(1)

	if err := n1.Elect(); err != nil {
		t.Fatalf("Elect failed: %v", err)
	}
	if !n1.IsLeader() {
		t.Fatal("node 1 should be leader with 5/5 grants")
	}
	if err := n1.Write("x=1"); err != nil {
		t.Fatalf("leader write rejected: %v", err)
	}
	if got := n1.Log(); len(got) != 1 || got[0] != "x=1" {
		t.Fatalf("unexpected log: %v", got)
	}
}

// 少数派分区（2/5）无法凑齐多数派授权，选举失败。
func TestMinorityPartitionCannotElect(t *testing.T) {
	c, _ := newTestCluster(5)
	c.Partition([]int{1, 2}, []int{3, 4, 5})

	if err := c.Node(1).Elect(); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("minority partition elect should fail with ErrNoQuorum, got %v", err)
	}
	if c.Node(1).IsLeader() {
		t.Fatal("minority partition node must not become leader")
	}
	if err := c.Node(3).Elect(); err != nil {
		t.Fatalf("majority partition elect should succeed: %v", err)
	}
}

// 授权互斥：租约未过期时，节点不得把授权转授给其他候选者（即使任期更高）。
func TestGrantExclusivityUntilExpiry(t *testing.T) {
	c, clk := newTestCluster(5)

	if err := c.Node(1).Elect(); err != nil {
		t.Fatalf("node 1 elect: %v", err)
	}
	// 租约未过期：节点 2 的更高任期选举无法获得任何授权。
	if err := c.Node(2).Elect(); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("elect before lease expiry should fail, got %v", err)
	}
	// 租约过期后：授权释放，新选举成功。
	clk.Advance(testLease + time.Second)
	if err := c.Node(2).Elect(); err != nil {
		t.Fatalf("elect after lease expiry should succeed: %v", err)
	}
	if c.Node(1).IsLeader() {
		t.Fatal("node 1 lease must be revoked after expiry")
	}
	if !c.Node(2).IsLeader() {
		t.Fatal("node 2 should be leader")
	}
}

// Leader 在租约过期前续订成功，主权限跨越多个租约周期保持有效。
func TestLeaseRenewal(t *testing.T) {
	c, clk := newTestCluster(5)
	n1 := c.Node(1)

	if err := n1.Elect(); err != nil {
		t.Fatalf("elect: %v", err)
	}
	// t=6s：租约未过期，续订成功，有效期延长到 t=16s。
	clk.Advance(6 * time.Second)
	if err := n1.Renew(); err != nil {
		t.Fatalf("renew: %v", err)
	}
	// t=12s：已超过首个租约的过期时间 t=10s，但续订使租约仍然有效。
	clk.Advance(6 * time.Second)
	if !n1.IsLeader() {
		t.Fatal("leader should still hold lease after renewal")
	}
	if err := n1.Write("y=2"); err != nil {
		t.Fatalf("write after renewal: %v", err)
	}
	// t=17s：续订租约也过期，主权限被自动剥离。
	clk.Advance(5 * time.Second)
	if n1.IsLeader() {
		t.Fatal("lease must be revoked after renewed lease expires")
	}
	if err := n1.Write("z=3"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("write after expiry should be rejected, got %v", err)
	}
}

// 验收场景：5 节点集群分区为 2+3。
//   - 3 节点分区：成功续订/重新选举，继续服务；
//   - 2 节点分区：无法凑齐多数派授权，旧 Leader 租约过期后自动剥离主权限；
//   - 任意时刻至多一个有效 Leader，杜绝脑裂。
func TestSplitBrainPrevention(t *testing.T) {
	c, clk := newTestCluster(5)
	oldLeader := c.Node(1)

	// 1. 全连通时选举 node 1 为 Leader 并接受写。
	if err := oldLeader.Elect(); err != nil {
		t.Fatalf("initial elect: %v", err)
	}
	if err := oldLeader.Write("before-partition"); err != nil {
		t.Fatalf("write before partition: %v", err)
	}

	// 2. 网络分区：{1,2} | {3,4,5}。
	c.Partition([]int{1, 2}, []int{3, 4, 5})

	// 3. 旧 Leader 在少数派分区续订：只能拿到 2/5 授权，续订失败。
	if err := oldLeader.Renew(); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("minority renew should fail with ErrNoQuorum, got %v", err)
	}
	// 既有租约尚未过期，旧 Leader 暂时仍有效（租约语义允许）。
	if !oldLeader.IsLeader() {
		t.Fatal("lease should remain valid until expiry")
	}

	// 4. 多数派分区在旧租约过期前发起选举：授权仍被 node 1 持有，必须失败。
	if err := c.Node(4).Elect(); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("majority elect before old lease expiry should fail, got %v", err)
	}

	// 5. 推进时钟越过租约过期时间：旧 Leader 主权限自动剥离，写请求被拒绝。
	clk.Advance(testLease + time.Second)
	if oldLeader.IsLeader() {
		t.Fatal("minority leader must lose leadership after lease expiry")
	}
	if err := oldLeader.Write("after-expiry"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("minority write after expiry should be rejected, got %v", err)
	}

	// 6. 多数派分区重新选举：node 4 获得 3/5 授权成为新 Leader 并服务写请求。
	newLeader := c.Node(4)
	if err := newLeader.Elect(); err != nil {
		t.Fatalf("majority elect after expiry: %v", err)
	}
	if err := newLeader.Write("after-partition"); err != nil {
		t.Fatalf("majority leader write: %v", err)
	}

	// 7. 脑裂检查：全集群至多一个有效 Leader。
	leaders := 0
	for _, id := range c.ids {
		if c.Node(id).IsLeader() {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("split brain detected: %d leaders", leaders)
	}
	if got := newLeader.Log(); len(got) != 1 || got[0] != "after-partition" {
		t.Fatalf("unexpected new leader log: %v", got)
	}
	if got := oldLeader.Log(); len(got) != 1 {
		t.Fatalf("minority leader must not accept writes after expiry, log: %v", got)
	}
}

// 非 Leader 节点一律拒绝写请求。
func TestNonLeaderRejectsWrites(t *testing.T) {
	c, _ := newTestCluster(5)
	if err := c.Node(3).Write("nope"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("follower write should be rejected, got %v", err)
	}
}

// 分区恢复后，少数派旧 Leader 无法复活；多数派新 Leader 可正常续订。
func TestHealDoesNotResurrectOldLeader(t *testing.T) {
	c, clk := newTestCluster(5)

	if err := c.Node(1).Elect(); err != nil {
		t.Fatalf("elect: %v", err)
	}
	c.Partition([]int{1, 2}, []int{3, 4, 5})
	clk.Advance(testLease + time.Second)
	if err := c.Node(4).Elect(); err != nil {
		t.Fatalf("majority elect: %v", err)
	}

	c.Heal()
	if c.Node(1).IsLeader() {
		t.Fatal("healed old leader must stay demoted")
	}
	clk.Advance(5 * time.Second)
	if err := c.Node(4).Renew(); err != nil {
		t.Fatalf("new leader renew after heal: %v", err)
	}
	if !c.Node(4).IsLeader() {
		t.Fatal("new leader should keep serving after heal")
	}
}
