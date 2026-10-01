// demo 复现验收边界：服务器 A 日志显示 12:00:00 发出请求，
// 服务器 B 日志显示 11:59:59 收到请求（物理时钟倒流，负延迟反例）。
// 重构器利用 RPC 四元组建立因果偏序，强制校正 B 的时钟基线，
// 输出物理自洽的调用拓扑图。
package main

import (
	"fmt"
	"time"

	"causalrecon"
)

func at(sec int, ms int) time.Time {
	base := time.Date(2026, 10, 2, 11, 59, 0, 0, time.UTC)
	return base.Add(time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond)
}

func main() {
	g := causal.NewGraph()

	// 原始日志：B 的物理时钟比 A 慢约 1.1s（未知漂移）。
	// A: 12:00:00.000 发请求，12:00:00.800 收响应（A 视角 RTT=800ms）。
	// B: 11:59:59.000 收请求，11:59:59.600 发响应（B 视角处理 600ms）。
	g.Add(causal.Event{ID: "a1", Server: "A", RPC: "rpc-1", Kind: causal.SendRequest, Raw: at(60, 0)})
	g.Add(causal.Event{ID: "b1", Server: "B", RPC: "rpc-1", Kind: causal.RecvRequest, Raw: at(59, 0)})
	g.Add(causal.Event{ID: "b2", Server: "B", RPC: "rpc-1", Kind: causal.SendResponse, Raw: at(59, 600)})
	g.Add(causal.Event{ID: "a2", Server: "A", RPC: "rpc-1", Kind: causal.RecvResponse, Raw: at(60, 800)})

	if err := g.Build(); err != nil {
		panic(err)
	}

	const minLatency = 1 * time.Millisecond

	fmt.Println("=== 校正前（原始物理时钟读数） ===")
	for _, e := range g.Events {
		fmt.Printf("  %-4s srv=%s rpc=%-6s %-9s raw=%s\n",
			e.ID, e.Server, e.RPC, e.Kind, e.Raw.Format("15:04:05.000"))
	}
	rawReq := g.Events[1].Raw.Sub(g.Events[0].Raw)
	fmt.Printf("  >> 请求网络延迟（原始读数直接相减）: %v  <-- 负延迟，物理上荒谬\n", rawReq)

	res, err := g.Reconstruct(minLatency)
	if err != nil {
		panic(err)
	}

	fmt.Println("\n=== 时钟基线校正量 ===")
	for _, srv := range []string{"A", "B"} {
		fmt.Printf("  srv=%s offset=%+v\n", srv, res.Offsets[srv])
	}

	fmt.Println("\n=== 校正后：物理自洽的调用拓扑（按校正时间排序） ===")
	for _, i := range res.Order {
		e := g.Events[i]
		fmt.Printf("  %-4s srv=%s rpc=%-6s %-9s corrected=%s\n",
			e.ID, e.Server, e.RPC, e.Kind, res.Corrected[i].Format("15:04:05.000"))
	}

	fmt.Println("\n=== 因果边（happens-before）校验 ===")
	for _, e := range g.Edges {
		u, v := g.Events[e.From], g.Events[e.To]
		d := res.Corrected[e.To].Sub(res.Corrected[e.From])
		kind := "program-order"
		if e.CrossHost {
			kind = "network     "
		}
		fmt.Printf("  %s(%s) -> %s(%s)  %-13s Δ=%v\n", u.ID, u.Server, v.ID, v.Server, kind, d)
	}
	if bad := g.Violations(res, minLatency); len(bad) > 0 {
		fmt.Printf("  !! 仍有 %d 条边违反因果\n", len(bad))
	} else {
		fmt.Println("  OK: 所有因果边均满足 物理时序自洽（跨机边 Δ >= 1ms）")
	}

	req, resp, total, err := g.RPCLatency(res, "rpc-1")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n=== rpc-1 校正后时延 ===\n  请求网络延迟=%v  响应网络延迟=%v  端到端=%v\n", req, resp, total)
}
