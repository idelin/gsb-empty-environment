package swim

import (
	"testing"
	"time"
)

const hop = 10 * time.Millisecond

// newCluster 构建 5 节点集群：A 直连 B 丢包（双向），其余链路通畅。
func newCluster() (map[string]*Node, *SimTransport, *ManualClock) {
	clock := NewManualClock()
	tr := NewSimTransport(clock, hop)
	tr.Drop = func(m Message) bool {
		// 弱网络：A 与 B 之间直连链路丢包，代理链路正常。
		direct := (m.From == "A" && m.To == "B") || (m.From == "B" && m.To == "A")
		return direct
	}
	nodes := map[string]*Node{}
	addrs := []string{"A", "B", "C", "D", "E"}
	for _, a := range addrs {
		n := NewNode(a, tr, clock)
		n.K = 2
		n.PingTimeout = 3 * hop
		n.IndirectTimeout = 6 * hop
		nodes[a] = n
		tr.Register(n)
	}
	for _, n := range nodes {
		n.Join(addrs...)
	}
	return nodes, tr, clock
}

func probeB(nodes map[string]*Node) {
	a := nodes["A"]
	a.SelectTarget = func(peers []string) string { return "B" }
	a.RunPeriod()
}

// 验收场景：A 直连 B 超时，触发 K=2 个代理（C/D/E 中任选两个）PingReq，
// 在时限内收到 Ack，B 不得被错误标记为 Down（保持 Alive）。
func TestIndirectProbePreventsFalsePositive(t *testing.T) {
	nodes, tr, clock := newCluster()
	a := nodes["A"]

	start := clock.Now()
	probeB(nodes)

	if got := a.State("B"); got != Alive {
		t.Fatalf("B 被误标记: got %s, want ALIVE", got)
	}
	if tr.Dropped == 0 {
		t.Fatal("预期 A->B 直连 Ping 丢包，但未观察到丢包")
	}
	// 直连超时上限 + 间接时限内完成：总耗时应远小于一个完整超时周期。
	elapsed := clock.Now().Sub(start)
	budget := a.PingTimeout + a.IndirectTimeout
	if elapsed > budget {
		t.Fatalf("探测超出时限: elapsed=%s budget=%s", elapsed, budget)
	}
	t.Logf("直连丢包 %d 次，经代理在 %s 内收到 Ack，B 保持 ALIVE", tr.Dropped, elapsed)
}

// 对照场景：B 完全隔离（代理也不通），应被正确标记 Suspect -> Down。
func TestFullyIsolatedNodeMarkedDown(t *testing.T) {
	clock := NewManualClock()
	tr := NewSimTransport(clock, hop)
	tr.Drop = func(m Message) bool {
		return m.From == "B" || m.To == "B"
	}
	addrs := []string{"A", "B", "C", "D"}
	nodes := map[string]*Node{}
	for _, addr := range addrs {
		n := NewNode(addr, tr, clock)
		nodes[addr] = n
		tr.Register(n)
	}
	for _, n := range nodes {
		n.Join(addrs...)
	}
	a := nodes["A"]
	a.SelectTarget = func(peers []string) string { return "B" }

	a.RunPeriod()
	if got := a.State("B"); got != Suspect {
		t.Fatalf("第一个周期后: got %s, want SUSPECT", got)
	}
	a.RunPeriod()
	if got := a.State("B"); got != Down {
		t.Fatalf("第二个周期后: got %s, want DOWN", got)
	}
}

// 直连通畅时不需要间接探测，一个周期内直接收到 Ack。
func TestDirectPingHealthy(t *testing.T) {
	clock := NewManualClock()
	tr := NewSimTransport(clock, hop)
	a := NewNode("A", tr, clock)
	b := NewNode("B", tr, clock)
	tr.Register(a)
	tr.Register(b)
	a.Join("B")
	b.Join("A")
	a.SelectTarget = func(peers []string) string { return "B" }

	target, ok := a.RunPeriod()
	if target != "B" || !ok {
		t.Fatalf("target=%s ok=%v, want B/true", target, ok)
	}
	if a.State("B") != Alive {
		t.Fatalf("B 应为 ALIVE, got %s", a.State("B"))
	}
}
