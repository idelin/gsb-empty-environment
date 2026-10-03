package main

import "testing"

// buildPartitionedSim 构造两个已分区并各自独立演化的子集群。
func buildPartitionedSim() *Sim {
	sim := NewSim()
	for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
		sim.AddNode(NewNode(id))
	}
	for _, n := range sim.nodes {
		for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
			n.Upsert(Member{ID: id, State: Alive, Version: 1})
		}
	}
	sim.Partition([]string{"A", "B", "C"}, []string{"D", "E", "F"})

	// 左侧：D 死亡 + 新成员 G
	for _, id := range []string{"A", "B", "C"} {
		sim.Node(id).Upsert(Member{ID: "D", State: Dead, Version: 2})
		sim.Node(id).Upsert(Member{ID: "G", State: Alive, Version: 1})
	}
	// 右侧：A 死亡 + 新成员 H
	for _, id := range []string{"D", "E", "F"} {
		sim.Node(id).Upsert(Member{ID: "A", State: Dead, Version: 2})
		sim.Node(id).Upsert(Member{ID: "H", State: Alive, Version: 1})
	}
	return sim
}

func membersEqual(a, b *Node) bool {
	ma, mb := a.Members(), b.Members()
	if len(ma) != len(mb) {
		return false
	}
	for i := range ma {
		if ma[i] != mb[i] {
			return false
		}
	}
	return true
}

// 验收：分区愈合后通过 4 字节 CRC 摘要察觉不一致，
// 5 个轮次内完成全量双向对齐。
func TestPartitionHealConvergesWithin5Rounds(t *testing.T) {
	sim := buildPartitionedSim()

	if sim.Node("A").Digest() == sim.Node("D").Digest() {
		t.Fatal("partitioned subclusters should have diverged digests")
	}

	sim.Heal()
	rounds := sim.RunUntilConverged(5)
	if rounds < 0 {
		t.Fatalf("did not converge within 5 rounds: %s", sim)
	}
	if rounds > 5 {
		t.Fatalf("converged in %d rounds, want <= 5", rounds)
	}
	t.Logf("converged in %d rounds: %s", rounds, sim)

	// 摘要一致之外，逐节点校验成员表全量双向对齐
	nodes := []string{"A", "B", "C", "D", "E", "F"}
	for _, id := range nodes[1:] {
		if !membersEqual(sim.Node("A"), sim.Node(id)) {
			t.Fatalf("node %s membership not aligned with A", id)
		}
	}

	// 校验合并结果符合预期：G、H 互相可见，状态按版本仲裁
	view := make(map[string]Member)
	for _, m := range sim.Node("A").Members() {
		view[m.ID] = m
	}
	if _, ok := view["G"]; !ok {
		t.Fatal("G from left subcluster missing after reconcile")
	}
	if _, ok := view["H"]; !ok {
		t.Fatal("H from right subcluster missing after reconcile")
	}
	if view["D"].State != Dead || view["D"].Version != 2 {
		t.Fatalf("D should be Dead@2, got %+v", view["D"])
	}
	if view["A"].State != Dead || view["A"].Version != 2 {
		t.Fatalf("A should be Dead@2, got %+v", view["A"])
	}
}

// 分区期间两侧内部各自保持一致，不应触发对账。
func TestNoReconcileWhenConsistent(t *testing.T) {
	sim := buildPartitionedSim()
	sim.RunRound()
	if sim.Reconciles != 0 {
		t.Fatalf("expected 0 reconciles within consistent subclusters, got %d", sim.Reconciles)
	}
	if sim.Probes == 0 {
		t.Fatal("probes should still run")
	}
}

// 愈合瞬间第一轮探活即通过摘要失配触发对账。
func TestHealTriggersImmediateDetection(t *testing.T) {
	sim := buildPartitionedSim()
	sim.Heal()
	sim.RunRound()
	if sim.Reconciles == 0 {
		t.Fatal("heal should trigger reconcile on first probe round")
	}
	if !sim.Converged() {
		t.Fatal("full-mesh heal should converge within the first round")
	}
}
