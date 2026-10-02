package discovery

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// waitFor 在 deadline 内轮询 cond，超时则报错。
func waitFor(t *testing.T, deadline time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

func knownIDs(n *Node) map[string]bool {
	ids := make(map[string]bool)
	for _, m := range n.Members() {
		ids[m.ID] = true
	}
	return ids
}

// 验收边界：3 个全新节点仅配置单点 Seed 地址，
// 1 秒内通过 Bootstrap 拉取全网 50 个节点元数据，并立即融入 Gossip 洗牌环。
func TestBootstrapColdStart(t *testing.T) {
	const existingMembers = 50

	seed := NewSeed()
	if err := seed.Start(); err != nil {
		t.Fatal(err)
	}
	defer seed.Close()

	// 预置一个已存在 50 个成员的集群（仅 Seed 持有权威快照）。
	for i := 0; i < existingMembers; i++ {
		seed.Register(NodeMeta{
			ID:        fmt.Sprintf("member-%02d", i),
			Addr:      fmt.Sprintf("127.0.0.1:%d", 20000+i),
			Heartbeat: time.Now().UnixNano(),
		})
	}

	// 3 个全新节点，仅配置单点 Seed 地址。
	nodes := make([]*Node, 3)
	start := time.Now()
	for i := range nodes {
		nodes[i] = NewNode(fmt.Sprintf("fresh-%d", i), []string{seed.Addr},
			WithGossipInterval(20*time.Millisecond), WithFanout(5))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := nodes[i].Bootstrap(ctx); err != nil {
			cancel()
			t.Fatalf("node %d bootstrap: %v", i, err)
		}
		cancel()
	}
	elapsed := time.Since(start)
	defer func() {
		for _, n := range nodes {
			n.Close()
		}
	}()

	// 1 秒内完成 Bootstrap 且拉取到 50 个节点元数据（+ 自身）。
	if elapsed > time.Second {
		t.Fatalf("bootstrap took %v, want < 1s", elapsed)
	}
	for i, n := range nodes {
		if got := n.MemberCount(); got < existingMembers+1 {
			t.Fatalf("node %d knows %d members after bootstrap, want >= %d",
				i, got, existingMembers+1)
		}
	}
	t.Logf("3 nodes bootstrapped in %v, each pulled %d member metadatas",
		elapsed, existingMembers)

	// 立即融入 Gossip 洗牌环：新节点应通过反熵交换互相发现，
	// 且 Seed 也应通过 gossip 学到全部新节点。
	want := []string{"fresh-0", "fresh-1", "fresh-2"}
	for i, n := range nodes {
		n := n
		waitFor(t, 2*time.Second, fmt.Sprintf("node %d discovers peers via gossip", i), func() bool {
			ids := knownIDs(n)
			for _, id := range want {
				if !ids[id] {
					return false
				}
			}
			return true
		})
	}
	waitFor(t, 2*time.Second, "seed learns all fresh nodes via gossip", func() bool {
		ids := make(map[string]bool)
		for _, m := range seed.Members() {
			ids[m.ID] = true
		}
		for _, id := range want {
			if !ids[id] {
				return false
			}
		}
		return true
	})
}

// 孤儿节点入网：首个 Seed 不可达时，握手器应依次尝试后续 Seed。
func TestBootstrapSeedFailover(t *testing.T) {
	seed := NewSeed()
	if err := seed.Start(); err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	seed.Register(NodeMeta{ID: "veteran", Addr: "127.0.0.1:21000", Heartbeat: 1})

	n := NewNode("orphan", []string{"127.0.0.1:1", seed.Addr}) // 第一个地址不可达
	defer n.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := n.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap with dead first seed: %v", err)
	}
	if !knownIDs(n)["veteran"] {
		t.Fatal("orphan node did not pull snapshot from backup seed")
	}
}

// 洗牌环：走完整环前不重复选取目标，耗尽后自动重洗。
func TestRingShuffle(t *testing.T) {
	r := NewRing()
	peers := []string{"a", "b", "c", "d", "e", "f"}
	r.Reset(peers)

	seen := make(map[string]int)
	for i := 0; i < 3; i++ {
		for _, p := range r.Next(2) {
			seen[p]++
		}
	}
	if len(seen) != len(peers) {
		t.Fatalf("ring covered %d/%d peers before reshuffle", len(seen), len(peers))
	}
	for _, p := range peers {
		if seen[p] != 1 {
			t.Fatalf("peer %s picked %d times in one ring pass", p, seen[p])
		}
	}
	if got := len(r.Next(2)); got != 2 {
		t.Fatalf("after full pass, Next returned %d peers, want 2 (reshuffled)", got)
	}
}
