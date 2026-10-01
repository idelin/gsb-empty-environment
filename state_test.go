package weightedvote

import "testing"

// 构造创世状态：四个投票人 A/B/C/D，初始权重 4/3/2/1。
func genesis() *State {
	g := NewState("boot")
	g.SetWeight("A", 4)
	g.SetWeight("B", 3)
	g.SetWeight("C", 2)
	g.SetWeight("D", 1)
	return g
}

// 模拟验收场景：四节点在网络分区下分别投票并动态变更权重，
// 返回分区两侧愈合前的四个独立副本。
func partitionScenario() (a, b, c, d *State) {
	base := genesis()

	// 分区 {A,B} 与 {C,D}，各自从创世状态分叉。
	a, b = base.Fork("node-A"), base.Fork("node-B")
	c, d = base.Fork("node-C"), base.Fork("node-D")

	// 分区期间两侧分别投票。
	a.CastVote("A", Yes)
	b.CastVote("B", Yes)
	c.CastVote("C", No)
	d.CastVote("D", Abstain)

	// 分区期间权重动态变更：
	//  - 无冲突：侧1 将 B 的权重 3->5；侧2 将 D 的权重 1->6。
	//  - 有冲突：两侧并发修改 C 的权重（侧1 改 4，侧2 改 1），
	//    Lamport 时钟相同，按节点 ID 决胜，node-C 的记录胜出 -> C=1。
	a.SetWeight("B", 5)
	c.SetWeight("D", 6)
	a.SetWeight("C", 4)
	c.SetWeight("C", 1)

	// 分区内各自同步（侧内合并）。
	a.Merge(b)
	b.Merge(a)
	c.Merge(d)
	d.Merge(c)
	return a, b, c, d
}

func heal(states ...*State) *State {
	merged := NewState("heal")
	for _, s := range states {
		merged.Merge(s)
	}
	return merged
}

// 验收：网络愈合后，合并逻辑正确计算全网有效总权重与弃权票，输出确定性决策。
func TestPartitionHealScenario(t *testing.T) {
	a, b, c, d := partitionScenario()
	merged := heal(a, b, c, d)

	tally := merged.Tally()
	if tally.Voters != 4 {
		t.Fatalf("Voters = %d, want 4", tally.Voters)
	}
	// 权重：A=4, B=5, C=1(冲突按节点ID决胜), D=6，总权重 16。
	if tally.TotalWeight != 16 {
		t.Fatalf("TotalWeight = %d, want 16", tally.TotalWeight)
	}
	if tally.YesWeight != 9 { // A(4) + B(5)
		t.Fatalf("YesWeight = %d, want 9", tally.YesWeight)
	}
	if tally.NoWeight != 1 { // C(1)
		t.Fatalf("NoWeight = %d, want 1", tally.NoWeight)
	}
	if tally.AbstainWeight != 6 { // D(6)
		t.Fatalf("AbstainWeight = %d, want 6", tally.AbstainWeight)
	}
	if got := tally.Decision(); got != Approved { // 9*2=18 > 16
		t.Fatalf("Decision = %s, want Approved", got)
	}

	// 并发权重冲突的确定性裁决：node-C 的记录（C=1）胜出。
	wrec, ok := merged.WeightOf("C")
	if !ok || wrec.Weight != 1 || wrec.Time.Node != "node-C" {
		t.Fatalf("WeightOf(C) = %+v, ok=%v, want weight=1 from node-C", wrec, ok)
	}
}

// 合并结果与合并顺序无关：遍历 4 个副本的全部 24 种合并排列，结论必须一致。
func TestMergeDeterministicAcrossOrders(t *testing.T) {
	a, b, c, d := partitionScenario()
	want := heal(a, b, c, d).Tally()

	states := []*State{a, b, c, d}
	perm := []int{0, 1, 2, 3}
	var check func(k int)
	check = func(k int) {
		if k == len(perm) {
			merged := NewState("heal")
			for _, i := range perm {
				merged.Merge(states[i])
			}
			if got := merged.Tally(); got != want {
				t.Fatalf("order %v: tally = %v, want %v", perm, got, want)
			}
			return
		}
		for i := k; i < len(perm); i++ {
			perm[k], perm[i] = perm[i], perm[k]
			check(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	check(0)
}

// 幂等：重复合并同一状态不改变结果。
func TestMergeIdempotent(t *testing.T) {
	a, _, c, _ := partitionScenario()
	m1 := heal(a, c)
	once := m1.Tally()
	m1.Merge(a)
	m1.Merge(c)
	m1.Merge(m1.Clone())
	if got := m1.Tally(); got != once {
		t.Fatalf("after re-merge tally = %v, want %v", got, once)
	}
}

// 交换律：A∪B 与 B∪A 结论一致。
func TestMergeCommutative(t *testing.T) {
	a, _, c, _ := partitionScenario()
	ab := heal(a, c)
	ba := heal(c, a)
	if ab.Tally() != ba.Tally() {
		t.Fatalf("a∪c = %v, c∪a = %v", ab.Tally(), ba.Tally())
	}
}

// 因果动态调整：同一节点上后发生的权重变更覆盖先发生的；
// 过时的远端记录不得覆盖较新的本地记录。
func TestCausalWeightOverride(t *testing.T) {
	s := NewState("node-1")
	s.SetWeight("A", 1)
	s.SetWeight("A", 7)
	if rec, _ := s.WeightOf("A"); rec.Weight != 7 {
		t.Fatalf("WeightOf(A) = %d, want 7", rec.Weight)
	}

	stale := NewState("node-0")
	stale.SetWeight("A", 99) // Lamport=1，落后于 s 的 Lamport=2
	s.Merge(stale)
	if rec, _ := s.WeightOf("A"); rec.Weight != 7 {
		t.Fatalf("after merging stale record, WeightOf(A) = %d, want 7", rec.Weight)
	}
}

// 并发冲突决胜：Lamport 时钟相同时按节点 ID 字典序取大者，结果确定。
func TestConcurrentConflictTiebreak(t *testing.T) {
	base := genesis()
	lo := base.Fork("node-A")
	hi := base.Fork("node-Z")
	lo.SetWeight("A", 100) // (5, node-A)
	hi.SetWeight("A", 200) // (5, node-Z)，节点 ID 更大，胜出

	m1, m2 := heal(lo, hi), heal(hi, lo)
	for i, m := range []*State{m1, m2} {
		rec, _ := m.WeightOf("A")
		if rec.Weight != 200 || rec.Time.Node != "node-Z" {
			t.Fatalf("merge %d: WeightOf(A) = %+v, want weight=200 from node-Z", i, rec)
		}
	}
}

// 决策门槛的边界行为。
func TestDecisionThresholds(t *testing.T) {
	cases := []struct {
		name             string
		yes, no, abstain uint64
		want             Decision
	}{
		{"empty", 0, 0, 0, Pending},
		{"yes strict majority", 3, 1, 1, Approved}, // 3*2>5
		{"yes exactly half", 2, 1, 1, Pending},     // 2*2==4 不通过，反对也未达半
		{"abstain raises bar", 2, 0, 3, Pending},   // 弃权计入总权重
		{"no reaches half", 1, 2, 1, Rejected},     // 2*2>=4
		{"no majority", 0, 3, 0, Rejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tally := Tally{
				Voters:        3,
				YesWeight:     tc.yes,
				NoWeight:      tc.no,
				AbstainWeight: tc.abstain,
				TotalWeight:   tc.yes + tc.no + tc.abstain,
			}
			if got := tally.Decision(); got != tc.want {
				t.Fatalf("Decision() = %s, want %s", got, tc.want)
			}
		})
	}
}

// 弃权票必须被正确汇总进总权重并影响门槛。
func TestAbstainCountedInTotal(t *testing.T) {
	s := NewState("node-1")
	s.SetWeight("A", 1)
	s.SetWeight("B", 1)
	s.CastVote("A", Yes)
	s.CastVote("B", Abstain)
	tally := s.Tally()
	if tally.TotalWeight != 2 || tally.AbstainWeight != 1 {
		t.Fatalf("tally = %v, want total=2 abstain=1", tally)
	}
	if got := tally.Decision(); got != Pending { // 1*2==2 未过半
		t.Fatalf("Decision = %s, want Pending", got)
	}
}
