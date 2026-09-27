package main

import "fmt"

func makeFlows(n int, rate float64) []Flow {
	flows := make([]Flow, n)
	for i := range flows {
		flows[i] = Flow{ID: uint64(i + 1), Rate: rate}
	}
	return flows
}

func flowIDs(flows []Flow) []uint64 {
	ids := make([]uint64, len(flows))
	for i, f := range flows {
		ids[i] = f.ID
	}
	return ids
}

func printFlowLoad(title string, paths []Path, assign map[uint64]string) {
	totalW := 0.0
	for _, p := range paths {
		if p.Weight > 0 {
			totalW += p.Weight
		}
	}
	load := LoadByPath(assign)
	fmt.Println(title)
	for _, p := range paths {
		got := float64(load[p.ID]) / float64(len(assign))
		want := 0.0
		if p.Weight > 0 {
			want = p.Weight / totalW
		}
		fmt.Printf("  路径 %-4s 权重 %4.1f  流数 %5d  占比 %6.2f%% (期望 %6.2f%%)\n",
			p.ID, p.Weight, load[p.ID], got*100, want*100)
	}
}

func main() {
	const numFlows = 10000
	flows := makeFlows(numFlows, 1.0)
	ids := flowIDs(flows)

	base := []Path{{"p1", 1}, {"p2", 2}, {"p3", 3}, {"p4", 4}}

	// 1) 初始分配
	a0 := NewAssigner(base)
	as0 := a0.AssignAll(ids)
	printFlowLoad("== 初始分配 (权重 1:2:3:4) ==", base, as0)

	// 2) 删除路径 p2：只有原在 p2 上的流迁移
	removed := []Path{{"p1", 1}, {"p3", 3}, {"p4", 4}}
	as1 := NewAssigner(removed).AssignAll(ids)
	moved, ratio := Migration(as0, as1)
	fmt.Printf("\n== 删除路径 p2 ==\n迁移流数 %d / %d，迁移比例 %.2f%% (p2 原占比 %.2f%%，其余路径 0 迁移)\n",
		moved, numFlows, ratio*100, float64(LoadByPath(as0)["p2"])/numFlows*100)

	// 3) 新增路径 p5(权重2)：只从旧路径吸流，旧路径之间不互迁
	added := []Path{{"p1", 1}, {"p3", 3}, {"p4", 4}, {"p5", 2}}
	as2 := NewAssigner(added).AssignAll(ids)
	moved2, ratio2 := Migration(as1, as2)
	fmt.Printf("\n== 新增路径 p5(权重2) ==\n迁移流数 %d / %d，迁移比例 %.2f%% (p5 新占比 %.2f%%)\n",
		moved2, numFlows, ratio2*100, float64(LoadByPath(as2)["p5"])/numFlows*100)
	printFlowLoad("新增后分配:", added, as2)

	// 4) 权重 0 路径不接新流
	withZero := []Path{{"p1", 1}, {"p3", 3}, {"p4", 4}, {"p5", 2}, {"p6", 0}}
	as3 := NewAssigner(withZero).AssignAll(ids)
	fmt.Printf("\n== 加入权重0路径 p6 ==\np6 分到流数: %d（权重0不接新流）\n", LoadByPath(as3)["p6"])

	// 5) 离散事件仿真：整数 tick，队列+链路容量
	fmt.Println("\n== 离散事件仿真 (200 tick, 每流 1 包/tick) ==")
	sim := NewSim(NewAssigner(added), added, flows, 1100.0) // 容量 = 权重 × 1100 包/tick，略大于需求
	sim.Run(200)
	total := 0
	for _, st := range sim.Stats() {
		total += st.Sent
	}
	for _, st := range sim.Stats() {
		fmt.Printf("  路径 %-4s 容量 %2d 包/tick  发送 %6d 包  负载占比 %6.2f%%  峰值积压 %5d 包\n",
			st.PathID, st.Capacity, st.Sent, st.Share*100, st.Backlog)
	}
	fmt.Printf("  合计发送 %d 包（无真实网络流量，纯内存事件模型）\n", total)
}
