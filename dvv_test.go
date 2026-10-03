package dvv

import (
	"fmt"
	"math/rand"
	"testing"
)

// 同一节点对同一键高频写入相同值（空更新）：每次写入用新的点状
// 标识取代旧值，集合始终保持单分支，不产生冗余分支。
func TestEmptyUpdatesStayMinimal(t *testing.T) {
	s := New()
	for i := 0; i < 1000; i++ {
		s = s.Update("A", []byte("v"))
	}
	if got := len(s.Entries); got != 1 {
		t.Fatalf("after 1000 identical rewrites: got %d branches, want 1", got)
	}
	branches, pairs := s.GraphSize()
	// 单节点：ctx={A:999} + dot A:1000 => 2 对，已是极简表示。
	if branches != 1 || pairs != 2 {
		t.Fatalf("graph size = (%d branches, %d pairs), want (1, 2)", branches, pairs)
	}
	if s.Entries[0].Dot != (Dot{Node: "A", Counter: 1000}) {
		t.Fatalf("dot = %v, want A:1000", s.Entries[0].Dot)
	}
}

// 高频覆盖更新（不同值顺序写）：旧分支被精确淘汰，版本图体积恒定。
func TestSequentialOverwritesStayMinimal(t *testing.T) {
	s := New()
	for i := 0; i < 500; i++ {
		s = s.Update("A", []byte(fmt.Sprintf("gen-%d", i)))
	}
	branches, pairs := s.GraphSize()
	if branches != 1 || pairs != 2 {
		t.Fatalf("graph size = (%d, %d), want (1, 2)", branches, pairs)
	}
	if got := string(s.Values()[0]); got != "gen-499" {
		t.Fatalf("value = %q, want gen-499", got)
	}
}

// 相同值的重复写入 vs 不同值的并发覆盖：DVV 必须精准区分。
// 前者收敛为单分支，后者保留为真实的并发兄弟分支。
func TestDuplicateVsConcurrent(t *testing.T) {
	// 相同值：两个副本各自广播同一逻辑写入的同一事件。
	w := New().Update("A", []byte("x"))
	replica1 := Join(w)
	replica2 := Join(w)
	merged := replica1.Sync(replica2)
	if len(merged.Entries) != 1 {
		t.Fatalf("duplicate same-value delivery: got %d branches, want 1", len(merged.Entries))
	}

	// 不同值并发覆盖：A 和 B 基于同一上下文并发写不同值。
	base := New().Update("A", []byte("base"))
	ctx := base.Context()
	r1 := base.Event(ctx, "A", []byte("from-A"))
	r2 := base.Event(ctx, "B", []byte("from-B"))
	merged = r1.Sync(r2)
	if len(merged.Entries) != 2 {
		t.Fatalf("concurrent different-value writes: got %d branches, want 2 siblings", len(merged.Entries))
	}
	vals := map[string]bool{}
	for _, v := range merged.Values() {
		vals[string(v)] = true
	}
	if !vals["from-A"] || !vals["from-B"] {
		t.Fatalf("siblings = %v, want both from-A and from-B", merged.Values())
	}

	// 读修复：客户端读到两个兄弟后做一次协调写，冲突消解回单分支。
	resolved := merged.Update("C", []byte("resolved"))
	if len(resolved.Entries) != 1 {
		t.Fatalf("after resolving write: got %d branches, want 1", len(resolved.Entries))
	}
	if got := string(resolved.Values()[0]); got != "resolved" {
		t.Fatalf("resolved value = %q", got)
	}
}

// 幽灵分支消除：与传统“每个值挂完整版本向量”的方案对比。
// 顺序写 A→B→A 下，朴素方案的旧值向量无法互相淘汰而残留分支，
// DVV 通过点状标识 + 共享上下文始终保持单分支。
func TestGhostBranchElimination(t *testing.T) {
	// 朴素版本向量：每个值携带写入时的完整 VV 快照，只按向量覆盖
	// 关系淘汰——向量相等但计数器不同的旧快照无法被精确识别。
	type naiveEntry struct {
		vv    VV
		value string
	}
	naive := []naiveEntry{}
	naiveWrite := func(node, value string, ctr uint64) {
		vv := VV{}
		for _, e := range naive {
			vv = vv.merge(e.vv)
		}
		vv[node] = ctr
		naive = append(naive, naiveEntry{vv: vv, value: value})
	}
	naiveWrite("A", "a1", 1)
	naiveWrite("B", "b1", 1)
	naiveWrite("A", "a2", 2)
	// 朴素方案：b1 的向量 {A:1,B:1} 与 a2 的 {A:2,B:1} 看似可覆盖，
	// 但 a1 的 {A:1} 等旧快照在无点状标识时无法与“已读未写”状态
	// 区分，保守实现只能保留 => 幽灵分支。
	if len(naive) != 3 {
		t.Fatalf("naive baseline should accumulate 3 branches, got %d", len(naive))
	}

	// DVV：同样的写入序列。
	s := New()
	s = s.Update("A", []byte("a1"))
	s = s.Update("B", []byte("b1"))
	s = s.Update("A", []byte("a2"))
	branches, pairs := s.GraphSize()
	if branches != 1 {
		t.Fatalf("DVV kept %d branches, want 1 (ghost branches eliminated)", branches)
	}
	// ctx={A:1,B:1} + dot A:2 => 3 对；朴素方案残留 3 条完整分支。
	if pairs != 3 {
		t.Fatalf("DVV context pairs = %d, want 3 (ctx A:1,B:1 + dot A:2)", pairs)
	}
	if got := string(s.Values()[0]); got != "a2" {
		t.Fatalf("value = %q, want a2", got)
	}
}

// 幂等性：同一事件乱序重复投递不改变集合。
func TestDuplicateDeliveryIdempotent(t *testing.T) {
	s := New().Update("A", []byte("v1"))
	event := s.Entries[0]
	r := New()
	for i := 0; i < 10; i++ {
		r = r.add(event)
	}
	if !r.Equal(s) {
		t.Fatalf("10 duplicate deliveries changed the set:\n%s", r)
	}
	if len(r.Entries) != 1 {
		t.Fatalf("branches = %d, want 1", len(r.Entries))
	}
}

// 多轮乱序网络广播下副本状态收敛。
func TestOutOfOrderBroadcastConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const replicas = 5

	// 三个节点并发写入（部分基于过期上下文，制造真实冲突与覆盖）。
	ctxA := New().Update("A", []byte("seed")).Context()
	writes := []*Set{
		New().Event(ctxA, "A", []byte("a1")),
		New().Event(ctxA, "B", []byte("b1")),
		New().Event(ctxA, "C", []byte("c1")),
	}
	// 基于汇合上下文的后续覆盖写。
	joined := Join(writes...)
	ctx2 := joined.Context()
	writes = append(writes,
		joined.Event(ctx2, "A", []byte("a2")),
		joined.Event(ctx2, "B", []byte("b2")),
	)

	// 收集所有事件作为“网络消息”，每个副本以独立随机顺序接收。
	var messages []Entry
	for _, w := range writes {
		messages = append(messages, w.Entries...)
	}

	final := Join(writes...)
	for r := 0; r < replicas; r++ {
		perm := rng.Perm(len(messages))
		rep := New()
		for _, idx := range perm {
			rep = rep.add(messages[idx])
			// 模拟重复广播。
			if rng.Intn(3) == 0 {
				rep = rep.add(messages[idx])
			}
		}
		if !rep.Equal(final) {
			t.Fatalf("replica %d diverged after out-of-order delivery:\ngot  %s\nwant %s", r, rep, final)
		}
	}

	// 最终状态：a2/b2 并发，a1/b1/c1 均被覆盖 => 恰好 2 个兄弟。
	if len(final.Entries) != 2 {
		t.Fatalf("final branches = %d, want 2 (a2, b2)", len(final.Entries))
	}
	branches, pairs := final.GraphSize()
	t.Logf("converged: branches=%d contextPairs=%d set=%s", branches, pairs, final)
}

// Join 的交换律、结合律与幂等律。
func TestJoinLaws(t *testing.T) {
	ctx := New().Update("A", []byte("s")).Context()
	a := New().Event(ctx, "A", []byte("1"))
	b := New().Event(ctx, "B", []byte("2"))
	c := New().Event(ctx, "C", []byte("3"))

	if !Join(a, b).Equal(Join(b, a)) {
		t.Fatal("join not commutative")
	}
	if !Join(Join(a, b), c).Equal(Join(a, Join(b, c))) {
		t.Fatal("join not associative")
	}
	if !Join(a, a).Equal(a) {
		t.Fatal("join not idempotent")
	}
}

// 混合负载压力：高频空更新 + 覆盖更新 + 乱序广播，版本图体积
// 始终保持极简（分支数 ≤ 真实并发度）。
func TestHighFrequencyMixedWorkload(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	nodes := []string{"A", "B", "C"}
	replicas := make([]*Set, 4)
	for i := range replicas {
		replicas[i] = New()
	}

	var inbox []Entry
	lastCtr := map[string]uint64{} // 每个节点为自己的写发放全局唯一序号
	for round := 0; round < 200; round++ {
		node := nodes[rng.Intn(len(nodes))]
		var value string
		if rng.Intn(2) == 0 {
			value = "constant" // 空更新：相同值重复写
		} else {
			value = fmt.Sprintf("overwrite-%d", round) // 覆盖更新
		}
		// 以某个随机副本的当前上下文做 leaderless 写入；写节点始终
		// 携带自己的最新序号，保证点状标识全局唯一。
		src := replicas[rng.Intn(len(replicas))]
		ctx := src.Context()
		ctx[node] = lastCtr[node]
		w := src.Event(ctx, node, []byte(value))
		lastCtr[node] = ctx[node] + 1
		inbox = append(inbox, w.Entries[len(w.Entries)-1])
	}

	// 乱序 + 重复投递到所有副本。
	want := New()
	for _, e := range inbox {
		want = want.add(e)
	}
	for i := range replicas {
		perm := rng.Perm(len(inbox))
		rep := New()
		for _, idx := range perm {
			rep = rep.add(inbox[idx])
			if rng.Intn(4) == 0 {
				rep = rep.add(inbox[idx])
			}
		}
		replicas[i] = rep
		if !rep.Equal(want) {
			t.Fatalf("replica %d diverged:\ngot  %s\nwant %s", i, rep, want)
		}
	}

	branches, pairs := want.GraphSize()
	if branches > len(nodes) {
		t.Fatalf("branches = %d exceeds max real concurrency %d", branches, len(nodes))
	}
	t.Logf("after 200 mixed writes + shuffled broadcast: branches=%d contextPairs=%d", branches, pairs)
}
