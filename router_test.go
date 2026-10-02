package leasehash

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// ---------- 红黑树单元测试 ----------

func verifyRBTree(t *testing.T, tree *rbTree) {
	t.Helper()
	// BST 有序性：中序遍历必须严格递增
	var prev uint64
	first := true
	count := 0
	tree.inOrder(func(k uint64, _ string) {
		if !first && k <= prev {
			t.Fatalf("BST order violation at key %d", k)
		}
		prev = k
		first = false
		count++
	})
	if count != tree.size {
		t.Fatalf("size mismatch: counted %d, recorded %d", count, tree.size)
	}
	// 红黑性质：根黑、无连续红、各路径黑高一致
	if tree.root != nil && tree.root.color != black {
		t.Fatal("root must be black")
	}
	var check func(n *rbNode, blackDepth int) int
	check = func(n *rbNode, blackDepth int) int {
		if n == nil {
			return blackDepth + 1
		}
		if n.color == red {
			if n.parent != nil && n.parent.color == red {
				t.Fatalf("red-red violation at key %d", n.key)
			}
		}
		if n.color == black {
			blackDepth++
		}
		l := check(n.left, blackDepth)
		r := check(n.right, blackDepth)
		if l != r {
			t.Fatalf("black-height violation at key %d", n.key)
		}
		return l
	}
	check(tree.root, 0)
}

func TestRBTreeInsertRemoveRandom(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	tree := &rbTree{}
	ref := make(map[uint64]string)
	// 随机插入
	for i := 0; i < 5000; i++ {
		k := rnd.Uint64()
		v := fmt.Sprintf("node-%d", i%7)
		tree.insert(k, v)
		ref[k] = v
		if i%97 == 0 {
			verifyRBTree(t, tree)
		}
	}
	verifyRBTree(t, tree)
	// 全部可读回
	for k, v := range ref {
		got, ok := tree.get(k)
		if !ok || got != v {
			t.Fatalf("get(%d) = %q,%v want %q", k, got, ok, v)
		}
	}
	// 随机删除一半
	i := 0
	for k := range ref {
		if i%2 == 0 {
			if !tree.remove(k) {
				t.Fatalf("remove(%d) failed", k)
			}
			delete(ref, k)
			if i%100 == 0 {
				verifyRBTree(t, tree)
			}
		}
		i++
	}
	verifyRBTree(t, tree)
	for k, v := range ref {
		got, ok := tree.get(k)
		if !ok || got != v {
			t.Fatalf("after remove get(%d) = %q,%v want %q", k, got, ok, v)
		}
	}
	// 删除剩余全部，树应为空
	for k := range ref {
		tree.remove(k)
	}
	if tree.len() != 0 || tree.root != nil {
		t.Fatalf("tree not empty after full removal: size=%d", tree.len())
	}
}

func TestRBTreeSuccessorWraps(t *testing.T) {
	tree := &rbTree{}
	keys := []uint64{10, 20, 30, 40}
	for _, k := range keys {
		tree.insert(k, fmt.Sprintf("n%d", k))
	}
	cases := []struct {
		hash uint64
		want uint64
	}{
		{0, 10}, {10, 10}, {15, 20}, {39, 40}, {40, 40},
		{41, 10},         // 越过最大值 -> 回绕到最小
		{^uint64(0), 10}, // 最大 uint64 -> 回绕
	}
	for _, c := range cases {
		succ, ok := tree.successor(c.hash)
		if !ok || succ.key != c.want {
			t.Fatalf("successor(%d) = %v want key %d", c.hash, succ, c.want)
		}
	}
}

// ---------- 寻址器测试 ----------

func newTestRouter() *Router {
	r := NewRouter(64, 3)
	for _, id := range []string{"A", "B", "C"} {
		if err := r.AddNode(id); err != nil {
			panic(err)
		}
	}
	return r
}

func keepAlive(r *Router, ids ...string) {
	for _, id := range ids {
		if err := r.Heartbeat(id); err != nil {
			panic(err)
		}
	}
}

// 验收场景：节点 C 心跳断开持续超过 3 个 tick，
// 寻址器原子将 C 的虚拟节点从红黑树环中抹去，
// 原属于 C 的 Key 瞬间透明平移给顺时针下一节点。
func TestLeaseExpiryAtomicallyEvictsNode(t *testing.T) {
	r := newTestRouter()
	const replicas = 64
	if got := r.RingSize(); got != 3*replicas {
		t.Fatalf("ring size = %d, want %d", got, 3*replicas)
	}

	var events []DownEvent
	r.OnNodeDown = func(ev DownEvent) { events = append(events, ev) }

	// tick 1、2：C 未心跳，但租约未耗尽，C 仍在环上
	for i := 0; i < 2; i++ {
		keepAlive(r, "A", "B")
		if evs := r.Tick(); len(evs) != 0 {
			t.Fatalf("tick %d: unexpected down events: %+v", i+1, evs)
		}
		if !r.IsOnline("C") {
			t.Fatalf("tick %d: C should still be online", i+1)
		}
	}

	// tick 3：C 连续 3 个 tick 未心跳，租约耗尽，被原子剔除
	keepAlive(r, "A", "B")
	evs := r.Tick()
	if len(evs) != 1 || evs[0].NodeID != "C" {
		t.Fatalf("tick 3: down events = %+v, want exactly C", evs)
	}
	if evs[0].VNodesRemoved != replicas {
		t.Fatalf("vnodes removed = %d, want %d", evs[0].VNodesRemoved, replicas)
	}
	if r.IsOnline("C") {
		t.Fatal("C should be offline after 3 missed ticks")
	}
	if got := r.RingSize(); got != 2*replicas {
		t.Fatalf("ring size after eviction = %d, want %d", got, 2*replicas)
	}
	// 环上不再存在任何属于 C 的虚拟节点
	for i := 0; i < replicas; i++ {
		if _, ok := r.OwnerOf(vnodeHash("C", i)); ok {
			t.Fatalf("vnode %d of C still on ring", i)
		}
	}
	// 回调与 Tick 返回值一致（下游重平衡已触发）
	if len(events) != 1 || events[0].NodeID != "C" {
		t.Fatalf("OnNodeDown callback events = %+v", events)
	}
}

// Key 平移语义：掉线后，原本路由到 C 的 key 全部改投其他在线节点，
// 且每个 key 的新属主恰是 C 对应虚拟节点顺时针方向的下一节点；
// 原本路由到 A/B 的 key 完全不受影响（最小扰动）。
func TestKeysShiftToClockwiseSuccessor(t *testing.T) {
	r := newTestRouter()

	// 采样一批 key，记录掉线前的路由结果
	const numKeys = 2000
	keys := make([]string, numKeys)
	before := make(map[string]string)
	onC := 0
	for i := range keys {
		keys[i] = fmt.Sprintf("user-session-%d", i)
		owner, err := r.Lookup(keys[i])
		if err != nil {
			t.Fatal(err)
		}
		before[keys[i]] = owner
		if owner == "C" {
			onC++
		}
	}
	if onC == 0 {
		t.Fatal("sample did not hit C; adjust replicas or key count")
	}
	t.Logf("keys on C before eviction: %d/%d", onC, numKeys)

	// 预先计算剔除 C 后的环拓扑，用于精确预测每个 key 的顺时针新属主
	r.mu.RLock()
	tmp := rbTree{}
	r.tree.inOrder(func(k uint64, v string) { tmp.insert(k, v) })
	for _, h := range r.nodes["C"].vnodes {
		tmp.remove(h)
	}
	r.mu.RUnlock()

	// C 掉线（3 tick 无心跳）
	for i := 0; i < 3; i++ {
		keepAlive(r, "A", "B")
		r.Tick()
	}

	migrated, untouched := 0, 0
	for _, k := range keys {
		after, err := r.Lookup(k)
		if err != nil {
			t.Fatal(err)
		}
		if after == "C" {
			t.Fatalf("key %q still routed to offline node C", k)
		}
		if before[k] == "C" {
			migrated++
			// 新属主必须是环上顺时针语义下的合法接管者（A 或 B）
			if after != "A" && after != "B" {
				t.Fatalf("key %q migrated to unexpected node %q", k, after)
			}
		} else {
			untouched++
			if after != before[k] {
				t.Fatalf("key %q on %s was disturbed: moved to %s", k, before[k], after)
			}
		}
	}
	if migrated != onC {
		t.Fatalf("migrated = %d, want %d", migrated, onC)
	}
	if untouched != numKeys-onC {
		t.Fatalf("untouched = %d, want %d", untouched, numKeys-onC)
	}
	t.Logf("migrated %d keys off C transparently; %d keys on A/B untouched", migrated, untouched)

	// 精确验证：每个 key 的实际路由 == 剔除 C 后的环上顺时针 successor
	for _, k := range keys {
		want, _ := tmp.successor(hash64(k))
		got, err := r.Lookup(k)
		if err != nil {
			t.Fatal(err)
		}
		if got != want.value {
			t.Fatalf("key %q routed to %q, clockwise successor predicts %q", k, got, want.value)
		}
	}
}

// 心跳续租：持续心跳的节点永不掉线。
func TestHeartbeatRenewsLease(t *testing.T) {
	r := newTestRouter()
	for i := 0; i < 20; i++ {
		keepAlive(r, "A", "B", "C")
		if evs := r.Tick(); len(evs) != 0 {
			t.Fatalf("tick %d: unexpected eviction: %+v", i, evs)
		}
	}
	for _, id := range []string{"A", "B", "C"} {
		if !r.IsOnline(id) {
			t.Fatalf("%s should remain online with heartbeats", id)
		}
	}
}

// 原子性：并发读请求在重平衡期间观察到的路由结果，
// 只能是"剔除前完整拓扑"或"剔除后完整拓扑"之一，
// 绝不出现 C 的部分虚拟节点残留的中间态。
func TestAtomicEvictionUnderConcurrency(t *testing.T) {
	r := newTestRouter()
	const replicas = 64

	stop := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan string, 16)

	// 并发读：持续 Lookup 并校验拓扑一致性
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("key-%d-%d", g, i)
				owner, err := r.Lookup(key)
				if err != nil && err != ErrNoOnlineNode {
					select {
					case errCh <- fmt.Sprintf("lookup error: %v", err):
					default:
					}
					return
				}
				// 校验：C 的虚拟节点要么全在、要么全不在
				r.mu.RLock()
				cCount := 0
				for v := 0; v < replicas; v++ {
					if _, ok := r.tree.get(vnodeHash("C", v)); ok {
						cCount++
					}
				}
				r.mu.RUnlock()
				if cCount != 0 && cCount != replicas {
					select {
					case errCh <- fmt.Sprintf("partial eviction observed: %d/%d vnodes of C", cCount, replicas):
					default:
					}
					return
				}
				if cCount == 0 && owner == "C" {
					select {
					case errCh <- "routed to C after full eviction":
					default:
					}
					return
				}
				i++
			}
		}(g)
	}

	// 主线程推进 tick，C 在第 3 个 tick 掉线
	for i := 0; i < 5; i++ {
		keepAlive(r, "A", "B")
		r.Tick()
	}
	close(stop)
	wg.Wait()

	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
	if r.IsOnline("C") {
		t.Fatal("C should be offline")
	}
}

// 空环与主动摘除。
func TestRemoveNodeAndEmptyRing(t *testing.T) {
	r := NewRouter(32, 3)
	if _, err := r.Lookup("k"); err != ErrNoOnlineNode {
		t.Fatalf("empty ring lookup err = %v", err)
	}
	r.AddNode("A")
	r.AddNode("B")
	if err := r.AddNode("A"); err != ErrNodeExists {
		t.Fatalf("dup add err = %v", err)
	}
	if err := r.RemoveNode("A"); err != nil {
		t.Fatal(err)
	}
	if r.IsOnline("A") || r.RingSize() != 32 {
		t.Fatalf("after remove: online=%v ring=%d", r.IsOnline("A"), r.RingSize())
	}
	if err := r.Heartbeat("A"); err != ErrNodeNotFound {
		t.Fatalf("heartbeat on removed node err = %v", err)
	}
	if err := r.RemoveNode("A"); err != ErrNodeNotFound {
		t.Fatalf("re-remove err = %v", err)
	}
	// 全部摘除后回到空环
	r.RemoveNode("B")
	if _, err := r.Lookup("k"); err != ErrNoOnlineNode {
		t.Fatalf("drained ring lookup err = %v", err)
	}
}

// 分布 sanity：三节点负载大致均衡（虚拟节点数足够时）。
func TestDistributionRoughlyBalanced(t *testing.T) {
	r := NewRouter(512, 3)
	for _, id := range []string{"A", "B", "C"} {
		r.AddNode(id)
	}
	counts := map[string]int{}
	for i := 0; i < 30000; i++ {
		owner, _ := r.Lookup(fmt.Sprintf("k%d", i))
		counts[owner]++
	}
	for _, id := range []string{"A", "B", "C"} {
		share := float64(counts[id]) / 30000.0
		if share < 0.2 || share > 0.5 {
			t.Fatalf("node %s share %.2f out of [0.2, 0.5]", id, share)
		}
	}
	t.Logf("distribution: A=%d B=%d C=%d", counts["A"], counts["B"], counts["C"])
}

// 掉线事件中的顺时针接管者列表应当有序且只含在线节点。
func TestDownEventSuccessors(t *testing.T) {
	r := newTestRouter()
	var ev DownEvent
	r.OnNodeDown = func(e DownEvent) { ev = e }
	for i := 0; i < 3; i++ {
		keepAlive(r, "A", "B")
		r.Tick()
	}
	if ev.VNodesRemoved != 64 || len(ev.Successors) != 64 {
		t.Fatalf("event = %+v", ev)
	}
	for _, s := range ev.Successors {
		if s != "A" && s != "B" {
			t.Fatalf("successor %q is not an online node", s)
		}
	}
}
