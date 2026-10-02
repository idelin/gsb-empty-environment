package leasering

import (
	"fmt"
	"sync"
	"testing"
)

func newTestRing() *Ring {
	r := New(WithVNodes(64), WithLeaseTicks(3))
	r.AddNode("A")
	r.AddNode("B")
	r.AddNode("C")
	return r
}

func sampleKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("user:%08d", i)
	}
	return keys
}

func ownership(t *testing.T, r *Ring, keys []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		owner, err := r.Locate(k)
		if err != nil {
			t.Fatalf("Locate(%q) failed: %v", k, err)
		}
		m[k] = owner
	}
	return m
}

// 验收边界：C 断开心跳超过 3 个 tick 后被原子剔除，
// 其 Key 瞬间透明平移给顺时针后继，其余 Key 归属不变。
func TestLeaseExpiryAtomicEviction(t *testing.T) {
	r := newTestRing()
	keys := sampleKeys(5000)
	before := ownership(t, r, keys)

	// 3 个 tick 内 C 仍持有租约，拓扑不变。
	for i := 0; i < 3; i++ {
		r.Heartbeat("A")
		r.Heartbeat("B")
		r.Tick(1)
	}
	if got := r.Nodes(); len(got) != 3 {
		t.Fatalf("after 3 ticks C must still hold its lease, nodes=%v", got)
	}
	if want := 3 * 64; r.VNodeCount() != want {
		t.Fatalf("vnode count = %d, want %d", r.VNodeCount(), want)
	}

	// 第 4 个 tick：C 静默超过 3 个 tick，被原子剔除。
	r.Heartbeat("A")
	r.Heartbeat("B")
	events := r.Tick(1)
	if len(events) != 1 || events[0].RemovedNode != "C" {
		t.Fatalf("expected exactly one eviction of C, got %+v", events)
	}
	if events[0].MovedVNodes != 64 {
		t.Fatalf("MovedVNodes = %d, want 64", events[0].MovedVNodes)
	}
	total := 0
	for succ, n := range events[0].Inherited {
		if succ == "C" {
			t.Fatalf("evicted node C must not inherit anything")
		}
		total += n
	}
	if total != 64 {
		t.Fatalf("inherited ranges = %d, want 64", total)
	}
	if got := r.Nodes(); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("nodes after eviction = %v, want [A B]", got)
	}
	if want := 2 * 64; r.VNodeCount() != want {
		t.Fatalf("vnode count = %d, want %d", r.VNodeCount(), want)
	}

	// 透明平移：原属 C 的 Key 改归 A/B，其余 Key 归属不变；全程无 Locate 失败。
	after := ownership(t, r, keys)
	moved, stable := 0, 0
	for _, k := range keys {
		if before[k] == "C" {
			moved++
			if after[k] == "C" {
				t.Fatalf("key %q still routed to evicted node C", k)
			}
		} else {
			stable++
			if after[k] != before[k] {
				t.Fatalf("key %q unnecessarily remapped %s -> %s", k, before[k], after[k])
			}
		}
	}
	if moved == 0 {
		t.Fatal("expected some keys on C")
	}
	t.Logf("moved=%d stable=%d (only C's keys remapped)", moved, stable)
}

// 原属 C 的 Key 必须落在 C 各虚拟节点的顺时针后继上。
func TestKeysShiftToClockwiseSuccessor(t *testing.T) {
	r := newTestRing()
	keys := sampleKeys(5000)

	for i := 0; i < 4; i++ {
		r.Heartbeat("A")
		r.Heartbeat("B")
		r.Tick(1) // C 静默 4 个 tick 后过期
	}

	// 手工重建“C 仍在”的环，逐 Key 验证新归属 == 去掉 C 后的顺时针后继。
	reference := New(WithVNodes(64), WithLeaseTicks(3))
	reference.AddNode("A")
	reference.AddNode("B")
	for _, k := range keys {
		got, err := r.Locate(k)
		if err != nil {
			t.Fatalf("Locate(%q): %v", k, err)
		}
		want, err := reference.Locate(k)
		if err != nil {
			t.Fatalf("reference Locate(%q): %v", k, err)
		}
		if got != want {
			t.Fatalf("key %q routed to %s, clockwise successor is %s", k, got, want)
		}
	}
}

// 边界：静默恰好 3 个 tick 不剔除，第 4 个 tick 才剔除。
func TestLeaseBoundaryExactlyThreeTicks(t *testing.T) {
	r := New(WithVNodes(8), WithLeaseTicks(3))
	r.AddNode("A")
	r.AddNode("C")
	for i := 1; i <= 3; i++ {
		r.Heartbeat("A")
		if ev := r.Tick(1); len(ev) != 0 {
			t.Fatalf("tick %d: unexpected eviction %+v", i, ev)
		}
	}
	if !contains(r.Nodes(), "C") {
		t.Fatal("C evicted too early: silence of exactly 3 ticks must be tolerated")
	}
	r.Heartbeat("A")
	if ev := r.Tick(1); len(ev) != 1 || ev[0].RemovedNode != "C" {
		t.Fatalf("tick 4: C must be evicted, got %+v", ev)
	}
}

// 持续心跳续约的节点永不掉线。
func TestHeartbeatKeepsNodeAlive(t *testing.T) {
	r := newTestRing()
	for i := 0; i < 50; i++ {
		for _, id := range []string{"A", "B", "C"} {
			if err := r.Heartbeat(id); err != nil {
				t.Fatalf("heartbeat %s: %v", id, err)
			}
		}
		if ev := r.Tick(2); len(ev) != 0 {
			t.Fatalf("unexpected eviction: %+v", ev)
		}
	}
	if got := r.Nodes(); len(got) != 3 {
		t.Fatalf("nodes = %v, want 3 alive", got)
	}
}

// 心跳恢复（重新入环）后节点重新参与寻址。
func TestRejoinAfterEviction(t *testing.T) {
	r := newTestRing()
	r.Heartbeat("A")
	r.Heartbeat("B")
	r.Tick(4)
	if contains(r.Nodes(), "C") {
		t.Fatal("C should be evicted")
	}
	r.AddNode("C")
	if !contains(r.Nodes(), "C") {
		t.Fatal("C should rejoin")
	}
	if err := r.Heartbeat("C"); err != nil {
		t.Fatalf("heartbeat after rejoin: %v", err)
	}
}

// 剔除与寻址并发：读者永远看到一致的环（配合 go test -race）。
func TestConcurrentLocateDuringEviction(t *testing.T) {
	r := newTestRing()
	keys := sampleKeys(200)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := keys[g%len(keys)]
				owner, err := r.Locate(k)
				if err != nil {
					continue // 环可能瞬时为空（实现中不会，但容错）
				}
				if owner != "A" && owner != "B" && owner != "C" {
					t.Errorf("Locate returned phantom node %q", owner)
					return
				}
			}
		}(g)
	}
	for i := 0; i < 10; i++ {
		r.Heartbeat("A")
		r.Heartbeat("B")
		r.Tick(1)
	}
	close(stop)
	wg.Wait()
	if contains(r.Nodes(), "C") {
		t.Fatal("C must be evicted after 10 silent ticks")
	}
}

// 红黑树在大量随机增删后保持有序且大小正确。
func TestRBTreeIntegrity(t *testing.T) {
	tree := &rbTree{}
	seed := uint32(12345)
	next := func() uint32 { // xorshift32，确定性伪随机
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return seed
	}
	keys := make(map[uint32]bool)
	for i := 0; i < 2000; i++ {
		h := next()
		if keys[h] {
			continue
		}
		keys[h] = true
		tree.insert(h, &vnode{hash: h, nodeID: "n"})
	}
	if tree.len() != len(keys) {
		t.Fatalf("tree size = %d, want %d", tree.len(), len(keys))
	}
	prev := uint32(0)
	first := true
	count := 0
	tree.inOrder(func(key uint32, v *vnode) {
		if !first && key <= prev {
			t.Fatalf("in-order traversal not sorted at %d after %d", key, prev)
		}
		first = false
		prev = key
		count++
	})
	if count != len(keys) {
		t.Fatalf("traversed %d nodes, want %d", count, len(keys))
	}
	deleted := 0
	for h := range keys {
		if deleted >= len(keys)/2 {
			break
		}
		tree.delete(tree.get(h))
		delete(keys, h)
		deleted++
	}
	if tree.len() != len(keys) {
		t.Fatalf("after deletes tree size = %d, want %d", tree.len(), len(keys))
	}
	for h := range keys {
		if tree.get(h) == nil {
			t.Fatalf("key %d missing after deletes", h)
		}
	}
	// ceiling 语义
	tree2 := &rbTree{}
	for _, h := range []uint32{10, 20, 30} {
		tree2.insert(h, &vnode{hash: h, nodeID: "n"})
	}
	if n := tree2.ceiling(15); n == nil || n.key != 20 {
		t.Fatalf("ceiling(15) = %v, want 20", n)
	}
	if n := tree2.ceiling(31); n != nil {
		t.Fatalf("ceiling(31) = %v, want nil", n)
	}
	if n := tree2.min(); n == nil || n.key != 10 {
		t.Fatalf("min() = %v, want 10", n)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
