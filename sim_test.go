package main

import (
	"net/netip"
	"testing"
)

func mkPkt(id, arrival int, dst string, port uint16, proto Protocol, size int) Packet {
	return Packet{
		ID: id, Src: netip.MustParseAddr("192.0.2.1"), Dst: netip.MustParseAddr(dst),
		Proto: proto, SrcPort: 49152, DstPort: port, SizeBytes: size, Arrival: arrival,
	}
}

func testCfg() SimConfig {
	return SimConfig{ServiceTicks: 2, LinkDelay: 3, BytesPerTick: 50, QueueCapacity: 4}
}

func TestSimAllowDeliveredDenyDropped(t *testing.T) {
	rules := []Rule{
		rule("web", "10.0.0.0/8", ProtoTCP, 80, 80, ActionAllow),
		rule("block", "10.0.0.0/8", ProtoTCP, 0, 65535, ActionDeny),
	}
	pkts := []Packet{
		mkPkt(0, 0, "10.0.0.1", 80, ProtoTCP, 100),
		mkPkt(1, 1, "10.0.0.1", 22, ProtoTCP, 100),
	}
	stats := NewSim(testCfg(), rules).Run(pkts)
	if stats.Delivered != 1 || stats.Dropped != 1 {
		t.Fatalf("应交付 1 丢弃 1，得到 delivered=%d dropped=%d", stats.Delivered, stats.Dropped)
	}
	if stats.Hits[0] != 1 || stats.Hits[1] != 1 {
		t.Fatalf("命中计数错误: %v", stats.Hits)
	}
}

func TestSimTiming(t *testing.T) {
	rules := []Rule{rule("any", "0.0.0.0/0", ProtoAny, 0, 65535, ActionAllow)}
	// arrival=5，service=2 -> 7；size=100/50=2 tick -> 9；delay=3 -> 12。
	sim := NewSim(testCfg(), rules)
	sim.Run([]Packet{mkPkt(0, 5, "10.0.0.1", 80, ProtoTCP, 100)})
	var deliveredAt = -1
	for _, e := range sim.Log {
		if e.Event == "delivered" {
			deliveredAt = e.Tick
		}
	}
	if deliveredAt != 12 {
		t.Fatalf("交付 tick 应为 12，得到 %d（日志 %v）", deliveredAt, sim.Log)
	}
}

func TestSimQueueFullDrops(t *testing.T) {
	rules := []Rule{rule("any", "0.0.0.0/0", ProtoAny, 0, 65535, ActionAllow)}
	cfg := testCfg()
	cfg.QueueCapacity = 1
	// service=2 tick，同一 tick 到达 3 个包：1 个在服务、1 个入队、1 个被丢。
	pkts := []Packet{
		mkPkt(0, 0, "10.0.0.1", 80, ProtoTCP, 60),
		mkPkt(1, 0, "10.0.0.2", 80, ProtoTCP, 60),
		mkPkt(2, 0, "10.0.0.3", 80, ProtoTCP, 60),
	}
	stats := NewSim(cfg, rules).Run(pkts)
	if stats.Dropped != 1 || stats.Delivered != 2 {
		t.Fatalf("应丢 1 交付 2，得到 dropped=%d delivered=%d", stats.Dropped, stats.Delivered)
	}
	if stats.MaxQueue != 1 {
		t.Fatalf("峰值队列应为 1，得到 %d", stats.MaxQueue)
	}
}

func TestSimDeterminism(t *testing.T) {
	rules := demoRules()
	rep := Analyze(rules)
	pkts := syntheticPackets(rep, 2)
	a := NewSim(SimConfig{ServiceTicks: 1, LinkDelay: 2, BytesPerTick: 64, QueueCapacity: 8}, rules)
	a.Run(pkts)
	b := NewSim(SimConfig{ServiceTicks: 1, LinkDelay: 2, BytesPerTick: 64, QueueCapacity: 8}, rules)
	b.Run(pkts)
	if len(a.Log) != len(b.Log) {
		t.Fatalf("两次运行日志长度不同: %d vs %d", len(a.Log), len(b.Log))
	}
	for i := range a.Log {
		if a.Log[i] != b.Log[i] {
			t.Fatalf("日志 %d 处不一致: %v vs %v", i, a.Log[i], b.Log[i])
		}
	}
}

// TestSimMatchesAnalysis 验证仿真命中结果与分析预测一致（合成反例的闭环校验）。
func TestSimMatchesAnalysis(t *testing.T) {
	rules := demoRules()
	rep := Analyze(rules)
	pkts := syntheticPackets(rep, 3)
	sim := NewSim(SimConfig{ServiceTicks: 1, LinkDelay: 2, BytesPerTick: 64, QueueCapacity: 8}, rules)
	stats := sim.Run(pkts)
	for i, rr := range rep.Rules {
		want := rr.RuleIndex
		if !rr.Reachable {
			want = rr.CoveredBy[0]
		}
		if got := Match(rules, pkts[i]); got != want {
			t.Errorf("pkt#%d 预测命中 #%d，实际 #%d", i, want, got)
		}
	}
	for i, rr := range rep.Rules {
		if !rr.Reachable && stats.Hits[rr.RuleIndex] != 0 {
			t.Errorf("被遮蔽规则 #%d 不应有命中，得到 %d", rr.RuleIndex, stats.Hits[rr.RuleIndex])
		}
		_ = i
	}
}
