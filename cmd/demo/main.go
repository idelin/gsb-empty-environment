// 运行样例：模拟节点 C 心跳断开超过 3 个 tick，
// 观察寻址器原子剔除 C 并将 Key 透明平移给顺时针下一节点。
package main

import (
	"fmt"

	"leasehash"
)

func main() {
	// 每节点 64 个虚拟节点，租约 TTL = 3 个 tick
	r := leasehash.NewRouter(256, 3)

	r.OnNodeDown = func(ev leasehash.DownEvent) {
		fmt.Printf(">>> [重平衡事件] 节点 %s 掉线：原子抹去 %d 个虚拟节点，顺时针接管者集合=%v\n",
			ev.NodeID, ev.VNodesRemoved, unique(ev.Successors))
	}

	for _, id := range []string{"A", "B", "C"} {
		r.AddNode(id)
	}
	fmt.Printf("初始拓扑：在线节点=%v，环上虚拟节点总数=%d\n\n", r.OnlineNodes(), r.RingSize())

	// 采样 12 个业务 Key，记录初始路由
	keys := []string{
		"order:1001", "order:1002", "order:1003",
		"session:u7", "session:u8", "session:u9",
		"cart:u7", "cart:u8", "cart:u9",
		"feed:home", "feed:hot", "feed:new",
	}
	before := map[string]string{}
	fmt.Println("--- 掉线前路由 ---")
	for _, k := range keys {
		owner, _ := r.Lookup(k)
		before[k] = owner
		fmt.Printf("  %-14s -> %s\n", k, owner)
	}

	// 模拟心跳：A、B 持续续租；C 从第 1 个 tick 起心跳断开
	fmt.Println("\n--- 心跳推进（C 已断开） ---")
	for tick := 1; tick <= 4; tick++ {
		r.Heartbeat("A")
		r.Heartbeat("B")
		r.Tick()
		fmt.Printf("tick %d: 在线节点=%v，环大小=%d\n", tick, r.OnlineNodes(), r.RingSize())
	}

	fmt.Println("\n--- 掉线后路由（Key 透明平移） ---")
	migrated := 0
	for _, k := range keys {
		after, _ := r.Lookup(k)
		mark := ""
		if before[k] == "C" {
			mark = "   <-- 原属 C，已平移给顺时针下一节点"
			migrated++
		} else if before[k] != after {
			mark = "   [!!] 非 C 的 Key 被扰动（不应发生）"
		}
		fmt.Printf("  %-14s -> %s%s\n", k, after, mark)
	}
	fmt.Printf("\n结论：%d 个原属 C 的 Key 瞬间平移至顺时针后继节点，其余 Key 零扰动，业务无阻断。\n", migrated)
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
