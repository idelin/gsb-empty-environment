package main

import (
	"fmt"
	"time"

	"swim/swim"
)

func main() {
	clock := swim.NewManualClock()
	tr := swim.NewSimTransport(clock, 10*time.Millisecond)
	// 弱网络：A 与 B 之间直连丢包，其余链路通畅。
	tr.Drop = func(m swim.Message) bool {
		return (m.From == "A" && m.To == "B") || (m.From == "B" && m.To == "A")
	}

	addrs := []string{"A", "B", "C", "D", "E"}
	nodes := map[string]*swim.Node{}
	for _, addr := range addrs {
		n := swim.NewNode(addr, tr, clock)
		n.K = 2
		nodes[addr] = n
		tr.Register(n)
	}
	for _, n := range nodes {
		n.Join(addrs...)
	}

	a := nodes["A"]
	a.SelectTarget = func(peers []string) string { return "B" }

	fmt.Println("=== SWIM 演示：A 直连 B 丢包，K=2 代理间接探测 ===")
	for period := 1; period <= 3; period++ {
		target, ok := a.RunPeriod()
		fmt.Printf("周期 %d: A 探测 %s -> 成功=%v, A 视图=%v (虚拟时间=%s)\n",
			period, target, ok, a.Members(), clock.Now())
	}
	fmt.Printf("网络统计: 发送=%d 投递=%d 丢包=%d\n", tr.Sent, tr.Delivered, tr.Dropped)
	fmt.Printf("结论: B 在 A 的视图中保持 %s，未被误判为 Down\n", a.State("B"))
}
