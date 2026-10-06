package main

import (
	"math/rand"
	"testing"
)

// 随机增删（含重复边与自环），每步都与全图 BFS 核对。
func TestRandomOpsAgainstBFS(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := NewGraph()
		idx := NewReachIndex(g)
		n := 10
		for i := 0; i < 300; i++ {
			u, v := rng.Intn(n), rng.Intn(n)
			if rng.Intn(100) < 55 {
				idx.Add(u, v)
			} else {
				idx.Remove(u, v)
			}
			if diff := idx.VerifyAgainst(g); len(diff) > 0 {
				t.Fatalf("seed=%d step=%d: mismatch %v", seed, i, diff)
			}
		}
	}
}

// 重复边：计数减到 0 之前删除不影响可达性。
func TestDuplicateEdges(t *testing.T) {
	g := NewGraph()
	idx := NewReachIndex(g)
	idx.Add(1, 2)
	idx.Add(1, 2)
	idx.Add(1, 2)
	if c := idx.Remove(1, 2); len(c) != 0 {
		t.Fatalf("expected no change while count>0, got %v", c)
	}
	if c := idx.Remove(1, 2); len(c) != 0 {
		t.Fatalf("expected no change while count>0, got %v", c)
	}
	c := idx.Remove(1, 2)
	if len(c) != 1 || c[0].Added || c[0].Pair != (Pair{1, 2}) {
		t.Fatalf("expected -(1,2), got %v", c)
	}
	// 删除不存在的边是 no-op
	if c := idx.Remove(1, 2); len(c) != 0 {
		t.Fatalf("expected no-op, got %v", c)
	}
}

// 自环与环使节点到自身可达（正长度路径）。
func TestSelfReachViaCycle(t *testing.T) {
	g := NewGraph()
	idx := NewReachIndex(g)
	idx.Add(1, 1) // 自环
	idx.Add(2, 3)
	idx.Add(3, 2) // 2<->3 环
	want := map[Pair]bool{{1, 1}: true, {2, 3}: true, {3, 2}: true, {2, 2}: true, {3, 3}: true}
	for _, p := range idx.Pairs() {
		if !want[p] {
			t.Fatalf("unexpected pair %v", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing pairs %v", want)
	}
}

// 删除桥边后，不能保留由环自我支撑的伪可达性。
func TestBridgeRemovalKillsCycleReachability(t *testing.T) {
	g := NewGraph()
	idx := NewReachIndex(g)
	// 环 1->2->3->1，桥 0->1
	idx.Add(1, 2)
	idx.Add(2, 3)
	idx.Add(3, 1)
	idx.Add(0, 1)
	// 0 可达 0,1,2,3（经环绕回 1 后再到不了 0 —— 0 不可达自身）
	assertReach := func(p Pair, want bool) {
		got := false
		for _, q := range idx.Pairs() {
			if q == p {
				got = true
			}
		}
		if got != want {
			t.Fatalf("pair %v: got %v want %v", p, got, want)
		}
	}
	assertReach(Pair{0, 1}, true)
	assertReach(Pair{0, 3}, true)
	assertReach(Pair{0, 0}, false)
	assertReach(Pair{1, 1}, true) // 环上自可达

	// 删除桥 0->1：0 失去全部可达性；环内部保持不变
	changes := idx.Remove(0, 1)
	removed := make(map[Pair]bool)
	for _, c := range changes {
		if c.Added {
			t.Fatalf("unexpected addition %v", c.Pair)
		}
		removed[c.Pair] = true
	}
	for _, p := range []Pair{{0, 1}, {0, 2}, {0, 3}} {
		if !removed[p] {
			t.Fatalf("expected removal of %v", p)
		}
	}
	assertReach(Pair{1, 1}, true)
	assertReach(Pair{2, 2}, true)
	assertReach(Pair{3, 3}, true)

	// 再断开环上一边 3->1：环自我支撑的正长度可达性必须全部消失
	idx.Remove(3, 1)
	for _, p := range []Pair{{1, 1}, {2, 2}, {3, 3}, {3, 2}, {2, 1}} {
		assertReach(p, false)
	}
	assertReach(Pair{1, 2}, true)
	assertReach(Pair{2, 3}, true)
	assertReach(Pair{1, 3}, true)
	if diff := idx.VerifyAgainst(g); len(diff) > 0 {
		t.Fatalf("mismatch %v", diff)
	}
}
