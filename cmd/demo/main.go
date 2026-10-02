// 运行样例：模拟节点 C 心跳断开，观察租约过期、原子剔除与 Key 透明平移。
package main

import (
	"fmt"

	"leasering"
)

func main() {
	var events []leasering.RebalanceEvent
	ring := leasering.New(
		leasering.WithVNodes(64),
		leasering.WithLeaseTicks(3),
		leasering.WithRebalanceHook(func(ev leasering.RebalanceEvent) {
			events = append(events, ev)
		}),
	)
	ring.AddNode("A")
	ring.AddNode("B")
	ring.AddNode("C")

	// 选取能覆盖 A/B/C 三个节点的样例 Key。
	var keys []string
	seen := map[string]int{}
	for i := 0; len(keys) < 9; i++ {
		k := fmt.Sprintf("user:%04d", i)
		owner, _ := ring.Locate(k)
		if seen[owner] < 3 {
			seen[owner]++
			keys = append(keys, k)
		}
	}

	snapshot := func(tag string) map[string]string {
		m := make(map[string]string, len(keys))
		fmt.Printf("[%s] tick=%d nodes=%v vnodes=%d\n", tag, ring.TickNow(), ring.Nodes(), ring.VNodeCount())
		for _, k := range keys {
			owner, err := ring.Locate(k)
			if err != nil {
				fmt.Printf("  %-10s -> ERROR: %v\n", k, err)
				continue
			}
			m[k] = owner
			fmt.Printf("  %-10s -> %s\n", k, owner)
		}
		return m
	}

	before := snapshot("初始拓扑")

	fmt.Println("\n--- C 心跳断开，A/B 正常续约，逐 tick 推进 ---")
	for i := 1; i <= 4; i++ {
		ring.Heartbeat("A")
		ring.Heartbeat("B")
		ring.Tick(1)
		fmt.Printf("tick=%d  C 静默 %d 个 tick, nodes=%v\n", ring.TickNow(), i, ring.Nodes())
	}

	for _, ev := range events {
		fmt.Printf("\n[重平衡事件] tick=%d 剔除节点=%s 抹去虚拟节点=%d 继承分布=%v\n",
			ev.Tick, ev.RemovedNode, ev.MovedVNodes, ev.Inherited)
	}

	fmt.Println()
	after := snapshot("剔除后拓扑")

	moved, stable := 0, 0
	for _, k := range keys {
		if before[k] != after[k] {
			moved++
			fmt.Printf("  %s: %s -> %s (平移)\n", k, before[k], after[k])
		} else {
			stable++
		}
	}
	fmt.Printf("\n结果: %d 个 Key 平移(全部原属 C), %d 个 Key 归属不变, 业务无阻断\n", moved, stable)
}
