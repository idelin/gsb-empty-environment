// 运行样例：租约协商状态机的离散 tick 模拟。
//
//	go run ./cmd/leasesim
package main

import (
	"fmt"

	"leasesim"
)

func main() {
	sim := leasesim.NewSim(leasesim.DefaultConfig())

	// 场景脚本。
	sim.At(100, func(s *leasesim.Sim) { s.StartElection(0) })
	sim.At(140, func(s *leasesim.Sim) {
		s.SetSpike(leasesim.Spike{Node: 0, Start: 140, End: 156, Extra: 30})
	})
	sim.At(145, func(s *leasesim.Sim) { s.Write(0) })
	sim.At(152, func(s *leasesim.Sim) { s.Write(0) })
	sim.At(160, func(s *leasesim.Sim) { s.Write(1) })

	sim.Run(200)

	fmt.Println("=== 事件日志（续租心跳汇总显示） ===")
	renewals := map[int]int{}
	lastRenewal := map[int]int{}
	for _, e := range sim.Events {
		if e.Kind == "renewed" {
			renewals[e.Node]++
			lastRenewal[e.Node] = e.Tick
			continue
		}
		fmt.Println(e)
	}
	for id := 0; id < len(sim.Nodes()); id++ {
		if n, ok := renewals[id]; ok {
			fmt.Printf("          node %d  %-13s lease renewed %d times by majority acks, last at tick %d\n",
				id, "renewed", n, lastRenewal[id])
		}
	}

	fmt.Println("=== 最终状态 ===")
	for _, n := range sim.Nodes() {
		fmt.Printf("node %d  role=%-9s epoch=%d\n", n.ID, n.Role, n.Epoch)
	}
}
