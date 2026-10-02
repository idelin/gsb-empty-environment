package merge

import (
	"testing"
	"time"
)

func viewOf(clusterID, epoch uint64, ids ...uint64) View {
	members := make(map[uint64]Member, len(ids))
	for _, id := range ids {
		members[id] = Member{ID: id, State: Alive, Incarnation: 1}
	}
	return View{ClusterID: clusterID, Epoch: epoch, Members: members}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

func TestLamportClockCorrection(t *testing.T) {
	var c LamportClock
	if got := c.Tick(); got != 1 {
		t.Fatalf("Tick = %d, want 1", got)
	}
	// 观测到更大的对端时钟：校正为 max+1。
	if got := c.Witness(41); got != 42 {
		t.Fatalf("Witness(41) = %d, want 42", got)
	}
	// 观测到更小的对端时钟：仅本地 +1。
	if got := c.Witness(10); got != 43 {
		t.Fatalf("Witness(10) = %d, want 43", got)
	}
}

func TestMergeKeyDeterministic(t *testing.T) {
	a := viewOf(1, 2, 1, 2, 3, 4, 5)
	b := viewOf(6, 3, 6, 7, 8, 9, 10)
	ka, kb := keyOf(a), keyOf(b)
	if ka.greater(kb) == kb.greater(ka) {
		t.Fatal("initiator election must have exactly one winner")
	}
	if !kb.greater(ka) {
		t.Fatal("higher epoch side should win")
	}
	// 纪元相同、规模相同：由 clusterID 打破平局，双方结论一致。
	c1 := viewOf(1, 1, 1, 2, 3)
	c2 := viewOf(6, 1, 6, 7, 8)
	if keyOf(c1).greater(keyOf(c2)) == keyOf(c2).greater(keyOf(c1)) {
		t.Fatal("tie-break must be deterministic")
	}
}

func TestDeriveClusterID(t *testing.T) {
	id1 := deriveClusterID(1, 6, 4)
	id2 := deriveClusterID(6, 1, 4) // 参数顺序无关
	if id1 != id2 {
		t.Fatal("deriveClusterID must be order-independent")
	}
	if id1 == 1 || id1 == 6 {
		t.Fatal("derived cluster ID must differ from both parents")
	}
	if deriveClusterID(1, 6, 4) == deriveClusterID(1, 6, 5) {
		t.Fatal("different epochs must derive different cluster IDs")
	}
}

func TestPartitionIsolation(t *testing.T) {
	net := NewNetwork()
	a := NewNode(1, net, []uint64{2}, viewOf(1, 1, 1))
	b := NewNode(2, net, []uint64{1}, viewOf(2, 1, 2))
	net.SetPartition([]uint64{1}, []uint64{2})

	env := Envelope{Type: MsgPingReq, From: 1, To: 2, ClusterID: 1, Epoch: 1}
	if net.Send(env) {
		t.Fatal("cross-partition send must be dropped")
	}
	if _, dropped := net.Stats(); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	net.Heal()
	if !net.Send(env) {
		t.Fatal("send must succeed after heal")
	}
	select {
	case got := <-b.inbox:
		if got.From != 1 {
			t.Fatalf("got message from %d, want 1", got.From)
		}
	default:
		t.Fatal("message not delivered after heal")
	}
	_ = a
}

func TestMergeCommitIdempotent(t *testing.T) {
	net := NewNetwork()
	n := NewNode(1, net, nil, viewOf(1, 1, 1, 2))
	commit := Envelope{
		Type:      MsgMergeCommit,
		From:      9,
		To:        1,
		ClusterID: 77,
		Epoch:     5,
		Members:   memberSlice(viewOf(77, 5, 1, 2, 3).Members),
	}
	n.handleMergeCommit(commit)
	n.handleMergeCommit(commit) // 重复投递：幂等
	v, _ := n.Snapshot()
	if v.ClusterID != 77 || v.Epoch != 5 || len(v.Members) != 3 {
		t.Fatalf("view = %+v, want cluster=77 epoch=5 members=3", v)
	}
	// 过期 Commit 必须忽略。
	stale := commit
	stale.Epoch = 4
	stale.ClusterID = 88
	n.handleMergeCommit(stale)
	v, _ = n.Snapshot()
	if v.ClusterID != 77 || v.Epoch != 5 {
		t.Fatalf("stale commit must be ignored, view = %+v", v)
	}
}

// TestSplitBrainMerge 是验收测试：
// 10 节点分裂为 [1-5] 与 [6-10] 各自独立演化，重连后首个 PingReq
// 成功触发合并握手，全网平滑收敛到单一视图且无死锁。
func TestSplitBrainMerge(t *testing.T) {
	groupA := []uint64{1, 2, 3, 4, 5}
	groupB := []uint64{6, 7, 8, 9, 10}
	seeds := append(append([]uint64{}, groupA...), groupB...)

	net := NewNetwork()
	var nodes []*Node
	for _, id := range groupA {
		nodes = append(nodes, NewNode(id, net, seeds, viewOf(1, 1, groupA...)))
	}
	for _, id := range groupB {
		nodes = append(nodes, NewNode(id, net, seeds, viewOf(6, 1, groupB...)))
	}
	for _, n := range nodes {
		n.Start()
	}
	defer func() {
		for _, n := range nodes {
			n.Stop() // Stop 返回即证明后台循环无死锁
		}
	}()

	// 阶段 1：分裂，两组独立演化（时钟发散、纪元独立推进）。
	net.SetPartition(groupA, groupB)
	nodes[0].NoteLocalFailure(3) // A 组纪元推进到 2
	nodes[5].NoteLocalFailure(8) // B 组纪元推进到 3
	nodes[5].NoteLocalFailure(9)
	time.Sleep(300 * time.Millisecond)

	// 分裂期间跨分区流量必须被丢弃。
	if _, dropped := net.Stats(); dropped == 0 {
		t.Fatal("expected cross-partition traffic to be dropped")
	}
	// 两组视图必须保持隔离：A 组节点不认识 B 组成员。
	for _, n := range nodes[:5] {
		v, _ := n.Snapshot()
		if _, ok := v.Members[6]; ok {
			t.Fatalf("node %d sees foreign member during partition", n.ID)
		}
	}

	var maxPreClock uint64
	for _, n := range nodes {
		_, clock := n.Snapshot()
		if clock > maxPreClock {
			maxPreClock = clock
		}
	}

	// 阶段 2：愈合网络，等待合并收敛。
	net.Heal()
	converged := func() (uint64, uint64, bool) {
		var cluster, epoch uint64
		for i, n := range nodes {
			v, _ := n.Snapshot()
			if len(v.Members) != 10 {
				return 0, 0, false
			}
			if i == 0 {
				cluster, epoch = v.ClusterID, v.Epoch
			} else if v.ClusterID != cluster || v.Epoch != epoch {
				return 0, 0, false
			}
		}
		return cluster, epoch, true
	}
	var finalCluster, finalEpoch uint64
	waitFor(t, 5*time.Second, "cluster to converge", func() bool {
		c, e, ok := converged()
		finalCluster, finalEpoch = c, e
		return ok
	})

	// 合并后集群必须是全新标识，且纪元不低于分裂前最大纪元。
	if finalCluster == 1 || finalCluster == 6 {
		t.Fatalf("merged cluster ID %d must differ from both parents", finalCluster)
	}
	if finalEpoch < 3 {
		t.Fatalf("merged epoch %d must be >= max pre-merge epoch 3", finalEpoch)
	}

	// 时钟校正：收敛后每个节点的时钟都不低于分裂前全网最大时钟。
	waitFor(t, 2*time.Second, "clocks to be corrected", func() bool {
		for _, n := range nodes {
			if _, clock := n.Snapshot(); clock < maxPreClock {
				return false
			}
		}
		return true
	})

	// 无活锁：收敛后纪元必须保持稳定（无无限合并/纪元膨胀）。
	time.Sleep(300 * time.Millisecond)
	for _, n := range nodes {
		v, _ := n.Snapshot()
		if v.Epoch != finalEpoch || v.ClusterID != finalCluster {
			t.Fatalf("node %d view unstable after convergence: %+v", n.ID, v)
		}
	}

	// 至少发生了一轮由发起方完成的合并。
	var rounds uint64
	for _, n := range nodes {
		rounds += n.MergeRounds()
	}
	if rounds == 0 {
		t.Fatal("expected at least one merge round")
	}
}
