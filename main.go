package main

import "fmt"

// 运行样例：3 个节点、带丢包/重复/随机延迟的网络，
// 写入 + 删除后周期性同步，展示收敛、墓碑传播与不动点。
func main() {
	cfg := Config{
		MinDelay:     1,
		MaxDelay:     6,
		DropRate:     0.15, // 15% 丢包
		DupRate:      0.05, // 5% 重复投递
		SyncInterval: 25,
	}
	sim := NewSim(cfg, 7)
	a := NewNode(sim, "A", "B")
	b := NewNode(sim, "B", "A", "C")
	NewNode(sim, "C", "B")
	c := sim.nodes["C"]

	// 初始写入：A 有 user-000..039，B 有 user-020..059，C 只有 config
	for i := 0; i < 40; i++ {
		a.Put(fmt.Sprintf("user-%03d", i), fmt.Sprintf("a-v%d", i))
	}
	for i := 20; i < 60; i++ {
		b.Put(fmt.Sprintf("user-%03d", i), fmt.Sprintf("b-v%d", i))
	}
	c.Put("config", "c1")

	// 删除：产生墓碑，必须传播而不是被对端旧值复活
	a.Delete("user-005")
	b.Delete("user-030")

	fmt.Printf("t=%d 初始: A=%d 条, B=%d 条, C=%d 条\n",
		sim.Now(), a.store.Len(), b.store.Len(), c.store.Len())

	sim.StartTimers()
	sim.Run(3000) // 在丢包/重复/延迟下运行约 120 轮同步
	sim.StopTimers()
	sim.RunUntilIdle()

	fmt.Printf("t=%d 收敛后: A=%d 条, B=%d 条, C=%d 条\n",
		sim.Now(), a.store.Len(), b.store.Len(), c.store.Len())
	fmt.Printf("收敛检查: A==B=%v, B==C=%v\n",
		a.store.Equal(b.store), b.store.Equal(c.store))

	for _, id := range []string{"A", "B", "C"} {
		r, _ := sim.nodes[id].store.Get("user-005")
		fmt.Printf("  节点 %s 上 user-005: %s\n", id, r)
	}

	// 不动点证据：再跑一轮完整同步，记录传输量必须为 0
	before := sim.Metrics.RecordsSent
	for _, id := range []string{"A", "B", "C"} {
		sim.nodes[id].SyncRound()
	}
	sim.RunUntilIdle()
	fmt.Printf("不动点验证: 额外一轮同步传输记录数 = %d（期望 0）\n",
		sim.Metrics.RecordsSent-before)

	m := sim.Metrics
	fmt.Printf("传输指标: 消息=%d (摘要=%d/%d 条目, 记录=%d/%d 条), 丢包=%d, 重复=%d\n",
		m.MsgsSent, m.DigestMsgs, m.DigestEntries, m.RecordMsgs, m.RecordsSent,
		m.Dropped, m.Duplicated)
}
