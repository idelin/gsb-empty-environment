package main

import "testing"

// 验收场景：3 节点通信数轮后，节点 A 依据矩阵时钟判定
// LSN=10 之前的所有事件已被全网接收，并安全截断物理日志。
func TestThreeNodeClusterGC(t *testing.T) {
	cluster := NewCluster("A", "B", "C")
	for round := 1; round <= 4; round++ {
		cluster.BroadcastRound("r")
	}

	a := cluster.Node("A")
	if len(a.log) != 16 {
		t.Fatalf("4 轮全互联后 A 应有 16 条日志，实际 %d", len(a.log))
	}

	// LSN<10 的每条事件都必须已被集群内每个节点确认。
	for _, e := range a.log {
		if e.LSN < 10 && !a.Acknowledged(e) {
			t.Fatalf("LSN=%d (origin=%s seq=%d) 未被全网确认", e.LSN, e.Origin, e.Seq)
		}
	}

	horizon, removed := a.TruncatePrefix()
	if horizon < 10 {
		t.Fatalf("截断水位应 >= 10，实际 %d", horizon)
	}
	if len(removed)+len(a.log) != 16 {
		t.Fatal("截断后日志条目数不守恒")
	}
	for _, e := range removed {
		if !a.Acknowledged(e) {
			t.Fatalf("被回收的 LSN=%d 不满足 GC 水位", e.LSN)
		}
	}
	for _, e := range a.log {
		if e.LSN <= horizon {
			t.Fatalf("截断后仍残留 LSN=%d <= 水位 %d", e.LSN, horizon)
		}
	}
	if len(a.log) > 0 && a.Acknowledged(a.log[0]) {
		t.Fatalf("前缀截断后首条日志 LSN=%d 仍可回收，说明截断未推进到底", a.log[0].LSN)
	}
}

// 未收敛的矩阵不得截断：单条消息后即触发 GC 应拒绝。
func TestNoTruncationBeforeConvergence(t *testing.T) {
	cluster := NewCluster("A", "B", "C")
	cluster.Send("A", "B", "only-one")

	b := cluster.Node("B")
	for _, e := range b.log {
		if b.Acknowledged(e) {
			t.Fatalf("C 尚未知悉任何事件，LSN=%d 不应被判为可回收", e.LSN)
		}
	}
	if horizon, removed := b.TruncatePrefix(); horizon != 0 || len(removed) != 0 {
		t.Fatalf("未收敛时不应截断，horizon=%d removed=%d", horizon, len(removed))
	}
}

// 日志截断是幂等推进的：再次截断不会误删未确认事件。
func TestTruncateIsMonotonic(t *testing.T) {
	cluster := NewCluster("A", "B", "C")
	cluster.BroadcastRound("r1")
	cluster.BroadcastRound("r2")

	a := cluster.Node("A")
	h1, _ := a.TruncatePrefix()
	before := len(a.log)
	h2, _ := a.TruncatePrefix()
	if h2 != 0 && h2 < h1 {
		t.Fatalf("水位不得回退：h1=%d h2=%d", h1, h2)
	}
	if len(a.log) > before {
		t.Fatal("截断后日志条数不应增加")
	}
}
