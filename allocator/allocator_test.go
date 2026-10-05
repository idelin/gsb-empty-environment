package allocator

import (
	"math/rand"
	"strings"
	"testing"
)

func shardList(prefix string, n int) []ShardID {
	shards := make([]ShardID, n)
	for i := range shards {
		shards[i] = ShardID(prefix + "-" + itoa(i))
	}
	return shards
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// assignRoundRobin 构造一个旧归属：把分片轮流分给给定节点。
func assignRoundRobin(shards []ShardID, ids ...NodeID) map[ShardID]NodeID {
	old := make(map[ShardID]NodeID)
	for i, s := range shards {
		old[s] = ids[i%len(ids)]
	}
	return old
}

func mustAllocate(t *testing.T, nodes []Node, shards []ShardID, old map[ShardID]NodeID) *Plan {
	t.Helper()
	plan, err := Allocate(nodes, shards, old)
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if err := Verify(plan, nodes, shards, old); err != nil {
		t.Fatalf("Verify failed: %v\n%s", err, plan.Proof())
	}
	if plan.Migration != plan.LowerBound {
		t.Fatalf("migration %d != lower bound %d", plan.Migration, plan.LowerBound)
	}
	return plan
}

func sumQuotas(plan *Plan) int {
	sum := 0
	for _, q := range plan.Quotas {
		sum += q
	}
	return sum
}

// 配额之和恒等于分片总数，且与理想值偏差小于 1（精确均衡而非近似）。
func TestQuotasExactAndBalanced(t *testing.T) {
	nodes := []Node{{"n1", 2}, {"n2", 1}, {"n3", 1}}
	shards := shardList("s", 10)
	plan := mustAllocate(t, nodes, shards, nil)
	if sumQuotas(plan) != 10 {
		t.Fatalf("quotas sum to %d, want 10", sumQuotas(plan))
	}
	// 理想值 5 / 2.5 / 2.5；余数并列时编号小者优先。
	want := map[NodeID]int{"n1": 5, "n2": 3, "n3": 2}
	for id, q := range want {
		if plan.Quotas[id] != q {
			t.Fatalf("node %s quota %d, want %d", id, plan.Quotas[id], q)
		}
	}
}

// 余数并列时严格按节点编号决胜。
func TestRemainderTieBreakByNodeID(t *testing.T) {
	nodes := []Node{{"n3", 1}, {"n1", 1}, {"n2", 1}}
	shards := shardList("s", 4) // 理想值各 4/3，余数并列，编号最小的 n1 多得 1
	plan := mustAllocate(t, nodes, shards, nil)
	if plan.Quotas["n1"] != 2 || plan.Quotas["n2"] != 1 || plan.Quotas["n3"] != 1 {
		t.Fatalf("tie-break wrong: %v", plan.Quotas)
	}
}

// 权重变化后只搬迁下界要求的最少分片，旧归属尽量保留。
func TestMinimalMigrationOnReweight(t *testing.T) {
	shards := shardList("s", 9)
	old := assignRoundRobin(shards, "n1", "n2", "n3") // 各 3 个
	nodes := []Node{{"n1", 5}, {"n2", 3}, {"n3", 1}}  // 新配额 5/3/1
	plan := mustAllocate(t, nodes, shards, old)
	if plan.Quotas["n1"] != 5 || plan.Quotas["n2"] != 3 || plan.Quotas["n3"] != 1 {
		t.Fatalf("quotas wrong: %v", plan.Quotas)
	}
	// 下界：n3 必须迁出 2 个，其余不动。
	if plan.Migration != 2 || plan.LowerBound != 2 {
		t.Fatalf("migration %d, lower bound %d; want both 2", plan.Migration, plan.LowerBound)
	}
	if plan.Stays != 7 {
		t.Fatalf("stays %d, want 7", plan.Stays)
	}
}

// 新增节点：迁入数恰好等于其配额，且总搬迁等于下界。
func TestNodeAdded(t *testing.T) {
	shards := shardList("s", 8)
	old := assignRoundRobin(shards, "n1", "n2") // 各 4 个
	nodes := []Node{{"n1", 1}, {"n2", 1}, {"n3", 1}}
	plan := mustAllocate(t, nodes, shards, old)
	// 配额 3/3/2（余数 2/3 并列，n1、n2 优先）。
	if plan.Quotas["n3"] != 2 {
		t.Fatalf("new node quota %d, want 2", plan.Quotas["n3"])
	}
	if plan.Migration != 2 || plan.LowerBound != 2 {
		t.Fatalf("migration %d, lower bound %d; want both 2", plan.Migration, plan.LowerBound)
	}
	for _, r := range plan.Nodes {
		if r.ID == "n3" && r.Arrived != 2 {
			t.Fatalf("new node arrived %d, want 2", r.Arrived)
		}
	}
}

// 节点下线：其上分片全部迁出，其余节点保留数不变，搬迁等于下界。
func TestNodeRemoved(t *testing.T) {
	shards := shardList("s", 9)
	old := assignRoundRobin(shards, "n1", "n2", "n3") // 各 3 个
	nodes := []Node{{"n1", 1}, {"n2", 1}}             // n3 下线
	plan := mustAllocate(t, nodes, shards, old)
	// n3 的 3 个分片必须迁出；n1/n2 配额 5/4，共需补 3 个，恰好接住。
	if plan.Migration != 3 || plan.LowerBound != 3 {
		t.Fatalf("migration %d, lower bound %d; want both 3", plan.Migration, plan.LowerBound)
	}
	for s, owner := range plan.Assignment {
		if owner == "n3" {
			t.Fatalf("shard %s still on removed node n3", s)
		}
	}
	if plan.Stays != 6 {
		t.Fatalf("stays %d, want 6", plan.Stays)
	}
}

// 权重归零：节点配额为 0，全部分片迁出，效果等同下线但节点仍在列表中。
func TestWeightZeroed(t *testing.T) {
	shards := shardList("s", 6)
	old := assignRoundRobin(shards, "n1", "n2", "n3") // 各 2 个
	nodes := []Node{{"n1", 1}, {"n2", 1}, {"n3", 0}}  // n3 权重归零
	plan := mustAllocate(t, nodes, shards, old)
	if plan.Quotas["n3"] != 0 {
		t.Fatalf("zeroed node quota %d, want 0", plan.Quotas["n3"])
	}
	if plan.Migration != 2 || plan.LowerBound != 2 {
		t.Fatalf("migration %d, lower bound %d; want both 2", plan.Migration, plan.LowerBound)
	}
	for s, owner := range plan.Assignment {
		if owner == "n3" {
			t.Fatalf("shard %s still on zero-weight node n3", s)
		}
	}
}

// 新增分片：计入 Arrived 但不计入 Migration，下界公式依然成立。
func TestNewShardsNotCountedAsMigration(t *testing.T) {
	old := assignRoundRobin(shardList("s", 6), "n1", "n2") // 各 3 个
	shards := append(shardList("s", 6), shardList("t", 3)...)
	nodes := []Node{{"n1", 1}, {"n2", 1}, {"n3", 1}} // 配额 3/3/3
	plan := mustAllocate(t, nodes, shards, old)
	if plan.NewShards != 3 {
		t.Fatalf("new shards %d, want 3", plan.NewShards)
	}
	// n1/n2 配额不变，旧分片一个都不用搬；n3 的 3 个全部来自新分片。
	if plan.Migration != 0 || plan.LowerBound != 0 {
		t.Fatalf("migration %d, lower bound %d; want both 0", plan.Migration, plan.LowerBound)
	}
}

// 确定性：打乱节点与分片输入顺序，结果完全一致。
func TestDeterministic(t *testing.T) {
	shards := shardList("s", 20)
	old := assignRoundRobin(shards, "n1", "n2", "n3")
	nodes := []Node{{"n1", 7}, {"n2", 2}, {"n3", 1}, {"n4", 0}}
	base := mustAllocate(t, nodes, shards, old)
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		shuffledNodes := append([]Node(nil), nodes...)
		shuffledShards := append([]ShardID(nil), shards...)
		rng.Shuffle(len(shuffledNodes), func(i, j int) { shuffledNodes[i], shuffledNodes[j] = shuffledNodes[j], shuffledNodes[i] })
		rng.Shuffle(len(shuffledShards), func(i, j int) { shuffledShards[i], shuffledShards[j] = shuffledShards[j], shuffledShards[i] })
		plan := mustAllocate(t, shuffledNodes, shuffledShards, old)
		for s, owner := range base.Assignment {
			if plan.Assignment[s] != owner {
				t.Fatalf("trial %d: shard %s owner %s, want %s", trial, s, plan.Assignment[s], owner)
			}
		}
	}
}

// Verify 能发现被篡改的方案（例如把某分片改到别的节点导致配额不符）。
func TestVerifyRejectsTampering(t *testing.T) {
	shards := shardList("s", 6)
	nodes := []Node{{"n1", 1}, {"n2", 1}}
	old := assignRoundRobin(shards, "n1", "n2")
	plan := mustAllocate(t, nodes, shards, old)
	plan.Assignment[shards[0]] = "n2"
	plan.Assignment[shards[1]] = "n2" // n2 超载、n1 欠载
	if err := Verify(plan, nodes, shards, old); err == nil {
		t.Fatal("Verify accepted a tampered plan")
	}
}

// 全部权重为零且存在分片时必须报错，而不是静默给出近似结果。
func TestAllZeroWeightsRejected(t *testing.T) {
	_, err := Allocate([]Node{{"n1", 0}, {"n2", 0}}, shardList("s", 3), nil)
	if err == nil {
		t.Fatal("expected error for all-zero weights")
	}
	// 没有分片时合法，所有配额为 0。
	plan, err := Allocate([]Node{{"n1", 0}}, nil, nil)
	if err != nil || plan.Quotas["n1"] != 0 {
		t.Fatalf("empty allocation failed: %v", err)
	}
}

// 随机化性质测试：任意节点集合、权重（含 0）、分片数与旧归属下，
// Verify 恒通过且搬迁数恒等于下界。
func TestPropertyMigrationAlwaysEqualsLowerBound(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		nodeCount := 1 + rng.Intn(6)
		nodes := make([]Node, nodeCount)
		ids := rng.Perm(30)[:nodeCount]
		for i := range nodes {
			nodes[i] = Node{ID: NodeID("n" + itoa(ids[i])), Weight: int64(rng.Intn(5))}
		}
		if trial%3 != 0 { // 保证多数用例总权重为正
			nodes[rng.Intn(nodeCount)].Weight = 1 + int64(rng.Intn(5))
		}
		shards := shardList("s", rng.Intn(40))
		// 旧归属：随机引用在线、已下线节点，部分分片缺失（视为新分片）。
		old := make(map[ShardID]NodeID)
		for _, s := range shards {
			switch rng.Intn(4) {
			case 0:
				continue // 新分片
			case 1:
				old[s] = "ghost" // 已下线节点
			default:
				old[s] = nodes[rng.Intn(nodeCount)].ID
			}
		}
		plan, err := Allocate(nodes, shards, old)
		allZero := true
		for _, n := range nodes {
			if n.Weight > 0 {
				allZero = false
			}
		}
		if allZero && len(shards) > 0 {
			if err == nil {
				t.Fatalf("trial %d: expected all-zero-weight error", trial)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: Allocate failed: %v", trial, err)
		}
		if err := Verify(plan, nodes, shards, old); err != nil {
			t.Fatalf("trial %d: Verify failed: %v\n%s", trial, err, plan.Proof())
		}
		if sumQuotas(plan) != len(shards) {
			t.Fatalf("trial %d: quotas sum %d, want %d", trial, sumQuotas(plan), len(shards))
		}
	}
}

// Proof 文本包含关键论证与逐节点明细。
func TestProofText(t *testing.T) {
	shards := shardList("s", 9)
	old := assignRoundRobin(shards, "n1", "n2", "n3")
	plan := mustAllocate(t, []Node{{"n1", 5}, {"n2", 3}, {"n3", 1}}, shards, old)
	proof := plan.Proof()
	for _, want := range []string{"下界", "min(旧持有_i, 配额_i)", "节点 n1"} {
		if !strings.Contains(proof, want) {
			t.Fatalf("proof missing %q:\n%s", want, proof)
		}
	}
	t.Logf("\n%s", proof)
}
