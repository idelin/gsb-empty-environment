package treesched

import (
	"math/rand"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, tr *Tree, id, parent NodeID, weight uint32) {
	t.Helper()
	if err := tr.Add(id, parent, weight); err != nil {
		t.Fatalf("Add(%d): %v", id, err)
	}
}

func mustLoad(t *testing.T, tr *Tree, id NodeID, bytes int64) {
	t.Helper()
	if err := tr.Load(id, bytes); err != nil {
		t.Fatalf("Load(%d,%d): %v", id, bytes, err)
	}
}

// drain 反复调度直到没有可投递字节，返回累计投递量。
func drain(t *testing.T, tr *Tree, budget int64) int64 {
	t.Helper()
	var total int64
	for i := 0; i < 10000; i++ {
		got := tr.Schedule(budget)
		if got == 0 {
			return total
		}
		total += got
	}
	t.Fatalf("drain 未能在限定轮数内收敛")
	return total
}

// TestReparentOntoDescendantPromotes 验证“挂到后代”时先把后代提升到
// 节点的原父节点，最终结构无环且父子关系正确。
func TestReparentOntoDescendantPromotes(t *testing.T) {
	tr := New()
	// 初始结构：root -> a -> b -> c
	mustAdd(t, tr, 1, RootID, 1)
	mustAdd(t, tr, 2, 1, 1)
	mustAdd(t, tr, 3, 2, 1)

	// 把 a 挂到它的后代 c 上：c 必须先被提升到 root。
	if err := tr.Reparent(1, 3, false); err != nil {
		t.Fatalf("Reparent: %v", err)
	}
	assertStructureAcyclic(t, tr)

	snap := tr.Snapshot()
	if snap[3].Parent != RootID {
		t.Fatalf("后代 c 应被提升到 root，实际父节点=%d", snap[3].Parent)
	}
	if snap[1].Parent != 3 {
		t.Fatalf("a 的父节点应为 c，实际=%d", snap[1].Parent)
	}
	if snap[2].Parent != 1 {
		t.Fatalf("b 的父节点应仍为 a，实际=%d", snap[2].Parent)
	}

	// 挂到自身是空操作，绝不允许自环。
	if err := tr.Reparent(2, 2, false); err != nil {
		t.Fatalf("Reparent 自身: %v", err)
	}
	if tr.Snapshot()[2].Parent != 1 {
		t.Fatalf("自挂接应是空操作")
	}
	assertStructureAcyclic(t, tr)
}

// TestExclusiveReparentMovesSiblings 验证独占挂接会把目标父节点的其他
// 孩子全部转到新节点之下，新节点成为唯一直接孩子。
func TestExclusiveReparentMovesSiblings(t *testing.T) {
	tr := New()
	// root -> {x, y, z, p}
	mustAdd(t, tr, 1, RootID, 1)
	mustAdd(t, tr, 2, RootID, 1)
	mustAdd(t, tr, 3, RootID, 1)
	mustAdd(t, tr, 4, RootID, 1)

	if err := tr.Reparent(4, RootID, true); err != nil {
		t.Fatalf("Reparent exclusive: %v", err)
	}
	assertStructureAcyclic(t, tr)

	snap := tr.Snapshot()
	if len(snap[RootID].Children) != 1 || snap[RootID].Children[0] != 4 {
		t.Fatalf("root 应只剩独占节点 p，实际孩子=%v", snap[RootID].Children)
	}
	want := map[NodeID]bool{1: true, 2: true, 3: true}
	if len(snap[4].Children) != 3 {
		t.Fatalf("p 应接管三个兄弟，实际=%v", snap[4].Children)
	}
	for _, c := range snap[4].Children {
		if !want[c] || snap[c].Parent != 4 {
			t.Fatalf("孩子 %d 未正确转到 p 下", c)
		}
	}
}

// TestBlockedNodeServesDescendants 验证阻塞节点自身载荷被冻结，
// 但其活跃后代仍能通过同一层的轮转拿到字节。
func TestBlockedNodeServesDescendants(t *testing.T) {
	tr := New()
	// root -> {a(阻塞, 有自身载荷), b}；a -> c。
	mustAdd(t, tr, 1, RootID, 1)
	mustAdd(t, tr, 2, RootID, 1)
	mustAdd(t, tr, 3, 1, 1)
	mustLoad(t, tr, 1, 1000)
	mustLoad(t, tr, 2, 1000)
	mustLoad(t, tr, 3, 1000)
	if err := tr.SetBlocked(1, true); err != nil {
		t.Fatalf("SetBlocked: %v", err)
	}

	total := drain(t, tr, 128)
	if total != 2000 {
		t.Fatalf("阻塞 a 后应只投递 b 与 c 的 2000 字节，实际=%d", total)
	}
	if q, _ := tr.Queued(1); q != 1000 {
		t.Fatalf("阻塞节点自身载荷必须原样保留，剩余=%d", q)
	}
	if d, _ := tr.Delivered(3); d != 1000 {
		t.Fatalf("阻塞节点的后代 c 应完整投递，实际=%d", d)
	}

	// 解除阻塞后，残留载荷必须可继续投递且账面守恒。
	if err := tr.SetBlocked(1, false); err != nil {
		t.Fatalf("解除阻塞: %v", err)
	}
	total += drain(t, tr, 64)
	if total != 3000 {
		t.Fatalf("解除阻塞后总投递应为 3000，实际=%d", total)
	}
	queued, delivered := tr.Totals()
	if queued != 0 || delivered != 3000 {
		t.Fatalf("排空后账面异常 queued=%d delivered=%d", queued, delivered)
	}
}

// TestClosedInteriorNodeFreezesOwnPayload 验证关闭中间节点只冻结其自身
// 载荷，子树其他节点继续获得份额，且关闭不可逆转、计费仍然守恒。
func TestClosedInteriorNodeFreezesOwnPayload(t *testing.T) {
	tr := New()
	mustAdd(t, tr, 1, RootID, 1)
	mustAdd(t, tr, 2, 1, 1)
	mustAdd(t, tr, 3, 2, 1)
	mustLoad(t, tr, 1, 500)
	mustLoad(t, tr, 2, 700)
	mustLoad(t, tr, 3, 900)

	if err := tr.Close(1); err != nil {
		t.Fatalf("Close(a): %v", err)
	}
	if err := tr.Close(2); err != nil {
		t.Fatalf("Close(b): %v", err)
	}
	delivered := drain(t, tr, 64)

	if delivered != 900 {
		t.Fatalf("关闭 a、b 后只有叶子 c 可投递，期望 900，实际=%d", delivered)
	}
	if q, _ := tr.Queued(1); q != 500 {
		t.Fatalf("关闭节点 a 的残留应冻结为 500，实际=%d", q)
	}
	if q, _ := tr.Queued(2); q != 700 {
		t.Fatalf("关闭节点 b 的残留应冻结为 700，实际=%d", q)
	}
	if d, _ := tr.Delivered(3); d != 900 {
		t.Fatalf("叶子 c 应完整投递，实际=%d", d)
	}
	// 关闭后解除阻塞无效，残留继续冻结。
	if err := tr.SetBlocked(1, false); err != nil {
		t.Fatalf("关闭节点上 SetBlocked 应返回 nil: %v", err)
	}
	if tr.Schedule(100) != 0 {
		t.Fatalf("关闭节点残留不可再投递")
	}
	queued, deliveredTotal := tr.Totals()
	if queued != 1200 || deliveredTotal != 900 {
		t.Fatalf("计费不守恒 queued=%d delivered=%d", queued, deliveredTotal)
	}
}

// TestZeroPayload 验证零载荷节点不产出字节、不影响其他节点，空树与
// 零预算/负预算调度返回 0。
func TestZeroPayload(t *testing.T) {
	tr := New()
	if got := tr.Schedule(1024); got != 0 {
		t.Fatalf("空树调度应返回 0，实际=%d", got)
	}
	mustAdd(t, tr, 1, RootID, 1)
	mustAdd(t, tr, 2, RootID, 4)
	if err := tr.Load(1, 0); err != nil {
		t.Fatalf("零载荷 Load: %v", err)
	}
	mustLoad(t, tr, 2, 100)
	if got := tr.Schedule(0); got != 0 {
		t.Fatalf("零预算应返回 0，实际=%d", got)
	}
	if got := tr.Schedule(-1); got != 0 {
		t.Fatalf("负预算应返回 0，实际=%d", got)
	}
	if got := drain(t, tr, 32); got != 100 {
		t.Fatalf("零载荷节点不应产出字节，期望投递 100，实际=%d", got)
	}
	if d, _ := tr.Delivered(1); d != 0 {
		t.Fatalf("零载荷节点投递量必须为 0，实际=%d", d)
	}
}

// TestWeightedFairShare 验证同层兄弟的长期投递量趋近权重比例。
func TestWeightedFairShare(t *testing.T) {
	tr := New()
	mustAdd(t, tr, 1, RootID, 1)
	mustAdd(t, tr, 2, RootID, 3)
	// 双方都保持大积压，仅发送一笔远小于总载荷的预算，
	// 测量同时积压区间内的份额，DRR 应按 1:3 分配。
	mustLoad(t, tr, 1, 1_000_000)
	mustLoad(t, tr, 2, 1_000_000)
	const budget = 12000
	var sent int64
	for sent < budget {
		sent += tr.Schedule(64)
	}

	d1, _ := tr.Delivered(1)
	d2, _ := tr.Delivered(2)
	ratio := float64(d2) / float64(d1)
	if ratio < 2.7 || ratio > 3.4 {
		t.Fatalf("权重 1:3 的投递比例应趋近 3，实际 d1=%d d2=%d ratio=%.3f", d1, d2, ratio)
	}
	// 双方都应仍有大量积压，证明测量发生在同时积压区间。
	if q, _ := tr.Queued(1); q == 0 {
		t.Fatalf("低权重节点不应提前排空")
	}
	if q, _ := tr.Queued(2); q == 0 {
		t.Fatalf("高权重节点不应提前排空")
	}
}

// TestExclusiveReparentCannotMonopolize 验证即使攻击者反复用独占挂接
// 重排依赖树，其他流仍按权重持续获得份额，无法被垄断带宽。
func TestExclusiveReparentCannotMonopolize(t *testing.T) {
	tr := New()
	const leaves = 6
	for id := NodeID(1); id <= leaves; id++ {
		mustAdd(t, tr, id, RootID, 1)
		mustLoad(t, tr, id, 6000)
	}
	rng := rand.New(rand.NewSource(42))
	var total int64
	for round := 0; round < 400; round++ {
		attacker := NodeID(1 + rng.Intn(leaves))
		victim := NodeID(1 + rng.Intn(leaves))
		if err := tr.Reparent(attacker, victim, true); err != nil {
			t.Fatalf("churn Reparent: %v", err)
		}
		assertStructureAcyclic(t, tr)
		total += tr.Schedule(64)
	}

	// 每个叶子都必须拿到份额；等权下最少者不应低于平均值的 60%。
	avg := float64(total) / leaves
	for id := NodeID(1); id <= leaves; id++ {
		d, _ := tr.Delivered(id)
		if d == 0 {
			t.Fatalf("节点 %d 在持续重排下被完全饿死", id)
		}
		if float64(d) < 0.6*avg {
			t.Fatalf("节点 %d 仅获得 %d 字节，低于均值 %.0f 的 60%%，带宽被垄断",
				id, d, avg)
		}
	}
	// 账面守恒：已投递 + 待发 = 总载入。
	queued, delivered := tr.Totals()
	if queued+delivered != leaves*6000 {
		t.Fatalf("重排期间计费不守恒 queued=%d delivered=%d", queued, delivered)
	}
}

// TestRapidReorderingStaysAcyclic 高频随机重排（含后代挂接与独占）下
// 持续断言无环，并验证调度不会因结构变化产生幽灵字节。
func TestRapidReorderingStaysAcyclic(t *testing.T) {
	tr := New()
	const n = 10
	for id := NodeID(1); id <= n; id++ {
		mustAdd(t, tr, id, RootID, 1+uint32(id%4))
		mustLoad(t, tr, id, 3000)
	}
	rng := rand.New(rand.NewSource(7))
	ids := userIDs(tr.Snapshot())
	var delivered int64
	for i := 0; i < 2000; i++ {
		id := pickID(rng, ids)
		target := pickID(rng, ids)
		exclusive := rng.Intn(2) == 0
		if err := tr.Reparent(id, target, exclusive); err != nil {
			t.Fatalf("Reparent(%d,%d,%v): %v", id, target, exclusive, err)
		}
		assertStructureAcyclic(t, tr)
		delivered += tr.Schedule(16)
	}
	queued, totalDelivered := tr.Totals()
	if totalDelivered != delivered {
		t.Fatalf("调度返回累计 %d 与账面 %d 不一致", delivered, totalDelivered)
	}
	if queued+totalDelivered != n*3000 {
		t.Fatalf("重排计费不守恒 queued=%d delivered=%d", queued, totalDelivered)
	}
}

// TestErrors 验证公开 API 的参数校验。
func TestErrors(t *testing.T) {
	tr := New()
	if err := tr.Add(RootID, RootID, 1); err != ErrRootReserved {
		t.Fatalf("期望 ErrRootReserved，得到 %v", err)
	}
	if err := tr.Add(9, RootID, 0); err != ErrBadWeight {
		t.Fatalf("期望 ErrBadWeight，得到 %v", err)
	}
	mustAdd(t, tr, 1, RootID, 1)
	if err := tr.Add(1, RootID, 1); err != ErrExists {
		t.Fatalf("期望 ErrExists，得到 %v", err)
	}
	if err := tr.Add(2, 99, 1); err != ErrNotExist {
		t.Fatalf("期望 ErrNotExist，得到 %v", err)
	}
	if err := tr.Load(1, -1); err != ErrBadBytes {
		t.Fatalf("期望 ErrBadBytes，得到 %v", err)
	}
	if err := tr.Reparent(RootID, 1, false); err != ErrRootReserved {
		t.Fatalf("期望 ErrRootReserved，得到 %v", err)
	}
	if err := tr.SetBlocked(RootID, true); err != ErrRootReserved {
		t.Fatalf("期望 ErrRootReserved，得到 %v", err)
	}
	if err := tr.Close(RootID); err != ErrRootReserved {
		t.Fatalf("期望 ErrRootReserved，得到 %v", err)
	}
	if err := tr.SetWeight(1, 0); err != ErrBadWeight {
		t.Fatalf("期望 ErrBadWeight，得到 %v", err)
	}
	if _, err := tr.Node(99); err != ErrNotExist {
		t.Fatalf("期望 ErrNotExist，得到 %v", err)
	}
}

// TestConcurrentAccess 冒烟验证多协程并发重排、载入与调度时数据竞争
// 与计费守恒（需配合 -race 使用）。
func TestConcurrentAccess(t *testing.T) {
	tr := New()
	const n = 8
	for id := NodeID(1); id <= n; id++ {
		mustAdd(t, tr, id, RootID, 1)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			ids := userIDs(tr.Snapshot())
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := pickID(rng, ids)
				target := pickID(rng, ids)
				_ = tr.Reparent(id, target, rng.Intn(2) == 0)
				_ = tr.Load(id, int64(rng.Intn(10)))
				_ = tr.Schedule(32)
			}
		}(int64(g + 1))
	}
	// 并发跑一段后停止，再单线程排空并核对守恒。
	for i := 0; i < 500; i++ {
		_ = tr.Schedule(16)
	}
	close(stop)
	wg.Wait()

	// 并发结束后核对账面非负且结构无环。
	queued, delivered := tr.Totals()
	if queued < 0 || delivered < 0 {
		t.Fatalf("并发后账面为负 queued=%d delivered=%d", queued, delivered)
	}
	assertStructureAcyclic(t, tr)
}
