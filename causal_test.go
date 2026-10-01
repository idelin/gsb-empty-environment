package causal

import (
	"testing"
	"time"
)

var base = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func ts(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }

const minLat = time.Millisecond

// 验收边界：A 12:00:00.000 发请求，B 11:59:59.000 收请求（时钟倒流）。
// 算法必须强制校正 B 的时钟基线，消除负延迟。
func TestNegativeLatencyCorrected(t *testing.T) {
	g := NewGraph()
	g.Add(Event{ID: "a1", Server: "A", RPC: "r1", Kind: SendRequest, Raw: ts(0)})
	g.Add(Event{ID: "b1", Server: "B", RPC: "r1", Kind: RecvRequest, Raw: ts(-1000)})
	g.Add(Event{ID: "b2", Server: "B", RPC: "r1", Kind: SendResponse, Raw: ts(-400)})
	g.Add(Event{ID: "a2", Server: "A", RPC: "r1", Kind: RecvResponse, Raw: ts(800)})

	if err := g.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := g.Reconstruct(minLat)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}

	if res.Offsets["A"] != 0 {
		t.Errorf("参照服务器 A 的校正量应为 0，实际 %v", res.Offsets["A"])
	}
	if res.Offsets["B"] < 1001*time.Millisecond {
		t.Errorf("B 的时钟基线应被强制校正至少 +1001ms，实际 %v", res.Offsets["B"])
	}

	// 校正后请求延迟必须 >= minLat（不再为负）。
	if d := res.Corrected[1].Sub(res.Corrected[0]); d < minLat {
		t.Errorf("校正后请求延迟 = %v，仍小于最小网络延迟", d)
	}
	// 全部因果边物理自洽。
	if bad := g.Violations(res, minLat); len(bad) != 0 {
		t.Errorf("仍有 %d 条因果边违反物理时序", len(bad))
	}
	// 拓扑序：发请求 < 收请求 < 发响应 < 收响应。
	want := []string{"a1", "b1", "b2", "a2"}
	for pos, idx := range res.Order {
		if g.Events[idx].ID != want[pos] {
			t.Fatalf("拓扑序第 %d 位应为 %s，实际 %s", pos, want[pos], g.Events[idx].ID)
		}
	}
	// 端到端时延保持 A 的本地观测（A 未校正）。
	_, _, total, err := g.RPCLatency(res, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if total != 800*time.Millisecond {
		t.Errorf("端到端时延应为 800ms，实际 %v", total)
	}
}

// 时钟本就一致的日志不应被改动。
func TestConsistentClocksUnchanged(t *testing.T) {
	g := NewGraph()
	g.Add(Event{ID: "a1", Server: "A", RPC: "r1", Kind: SendRequest, Raw: ts(0)})
	g.Add(Event{ID: "b1", Server: "B", RPC: "r1", Kind: RecvRequest, Raw: ts(50)})
	g.Add(Event{ID: "b2", Server: "B", RPC: "r1", Kind: SendResponse, Raw: ts(120)})
	g.Add(Event{ID: "a2", Server: "A", RPC: "r1", Kind: RecvResponse, Raw: ts(200)})

	if err := g.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := g.Reconstruct(minLat)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	for srv, off := range res.Offsets {
		if off != 0 {
			t.Errorf("服务器 %s 不应被校正，实际 offset=%v", srv, off)
		}
	}
}

// 三机调用链 A->B->C，漂移沿因果链累积传播。
func TestMultiServerChain(t *testing.T) {
	g := NewGraph()
	// A -> B：B 慢 1s。
	g.Add(Event{ID: "a1", Server: "A", RPC: "r1", Kind: SendRequest, Raw: ts(0)})
	g.Add(Event{ID: "b1", Server: "B", RPC: "r1", Kind: RecvRequest, Raw: ts(-1000)})
	// B -> C：C 慢 2s（相对 A）。
	g.Add(Event{ID: "b2", Server: "B", RPC: "r2", Kind: SendRequest, Raw: ts(-900)})
	g.Add(Event{ID: "c1", Server: "C", RPC: "r2", Kind: RecvRequest, Raw: ts(-2000)})
	g.Add(Event{ID: "c2", Server: "C", RPC: "r2", Kind: SendResponse, Raw: ts(-1950)})
	g.Add(Event{ID: "b3", Server: "B", RPC: "r2", Kind: RecvResponse, Raw: ts(-800)})
	g.Add(Event{ID: "b4", Server: "B", RPC: "r1", Kind: SendResponse, Raw: ts(-700)})
	g.Add(Event{ID: "a2", Server: "A", RPC: "r1", Kind: RecvResponse, Raw: ts(500)})

	if err := g.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := g.Reconstruct(minLat)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if res.Offsets["B"] < 1001*time.Millisecond {
		t.Errorf("B 校正量不足: %v", res.Offsets["B"])
	}
	// C 相对 B 又慢约 1.1s，故 C 的总校正量应显著大于 B。
	if res.Offsets["C"] <= res.Offsets["B"] {
		t.Errorf("C 的校正量(%v)应大于 B(%v)", res.Offsets["C"], res.Offsets["B"])
	}
	if bad := g.Violations(res, minLat); len(bad) != 0 {
		t.Errorf("仍有 %d 条因果边违反物理时序", len(bad))
	}
}

// 响应方向的负延迟同样必须被纠正。
func TestResponseNegativeLatencyCorrected(t *testing.T) {
	g := NewGraph()
	g.Add(Event{ID: "a1", Server: "A", RPC: "r1", Kind: SendRequest, Raw: ts(0)})
	g.Add(Event{ID: "b1", Server: "B", RPC: "r1", Kind: RecvRequest, Raw: ts(10)})
	g.Add(Event{ID: "b2", Server: "B", RPC: "r1", Kind: SendResponse, Raw: ts(30)})
	g.Add(Event{ID: "a2", Server: "A", RPC: "r1", Kind: RecvResponse, Raw: ts(25)}) // 早于 b2：响应负延迟

	if err := g.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := g.Reconstruct(minLat)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if d := res.Corrected[3].Sub(res.Corrected[2]); d < minLat {
		t.Errorf("校正后响应延迟 = %v，仍小于最小网络延迟", d)
	}
	if bad := g.Violations(res, minLat); len(bad) != 0 {
		t.Errorf("仍有 %d 条因果边违反物理时序", len(bad))
	}
}

// 自相矛盾的日志（B 的处理时间超过 A 观测到的 RTT）应报不可满足。
func TestInfeasibleLogsDetected(t *testing.T) {
	g := NewGraph()
	g.Add(Event{ID: "a1", Server: "A", RPC: "r1", Kind: SendRequest, Raw: ts(0)})
	g.Add(Event{ID: "b1", Server: "B", RPC: "r1", Kind: RecvRequest, Raw: ts(10)})
	g.Add(Event{ID: "b2", Server: "B", RPC: "r1", Kind: SendResponse, Raw: ts(500)}) // B 处理 490ms
	g.Add(Event{ID: "a2", Server: "A", RPC: "r1", Kind: RecvResponse, Raw: ts(100)}) // 但 A 的 RTT 只有 100ms

	if err := g.Build(); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := g.Reconstruct(minLat); err != ErrInfeasible {
		t.Errorf("应返回 ErrInfeasible，实际 %v", err)
	}
}

// 缺少配对事件的 RPC 应在 Build 阶段报错。
func TestUnpairedRPCRejected(t *testing.T) {
	g := NewGraph()
	g.Add(Event{ID: "a1", Server: "A", RPC: "r1", Kind: SendRequest, Raw: ts(0)})
	if err := g.Build(); err == nil {
		t.Error("缺少 RecvRequest 的 RPC 应报错")
	}
}
