package main

import "fmt"

// 演示：所有场景都在单进程事件模拟器中运行，
// 通信延迟、丢包故障、时间流逝全部由模拟器控制，结果完全可复现。

func printStates(reps []*Replica, key string) {
	for _, r := range reps {
		fmt.Printf("    副本%d: %s\n", r.ID, r.Get(key))
	}
}

func scenarioMerge() {
	fmt.Println("--- 场景1: 同键 100 次读，修复按键合并 ---")
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1) // 恒定延迟 10，完全确定
	reps := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	rm := NewRepairManager(sim, net, reps, 4, 3, 50)
	c := NewCoordinator(sim, net, reps, rm, 1000)

	for _, r := range reps {
		r.Apply("k", VersionedValue{"v1", 1})
	}
	reps[0].Apply("k", VersionedValue{"v2", 2}) // 副本1、2 落后
	fmt.Println("  初始: 副本0=(v2,ver2)，副本1/2=(v1,ver1) 落后")

	done := 0
	for i := 0; i < 100; i++ {
		c.Read("k", func(best VersionedValue, found bool) { done++ })
	}
	sim.Run()
	fmt.Printf("  100 次读全部完成(done=%d)\n", done)
	fmt.Printf("  修复 RPC 总数 = %d（100 次读的重复承诺被合并为每副本 1 次）\n", net.Sent[MsgRepairReq])
	fmt.Printf("  修复结果: %+v\n", rm.Outcomes)
	printStates(reps, "k")
}

func scenarioSafety() {
	fmt.Println("--- 场景2: 修复到达前出现新版本，旧修复不覆盖新写（安全性）---")
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1)
	reps := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	rm := NewRepairManager(sim, net, reps, 4, 3, 50)
	c := NewCoordinator(sim, net, reps, rm, 1000)

	for _, r := range reps {
		r.Apply("k", VersionedValue{"v1", 1})
	}
	reps[0].Apply("k", VersionedValue{"v2", 2})

	c.Read("k", nil) // t=0 发起读，t=20 承诺修复 v2，t=30 修复到达
	sim.After(25, func() {
		reps[1].Apply("k", VersionedValue{"v3", 3}) // 修复在途时，副本1 收到更新的写
		fmt.Println("  t=25: 副本1 收到新写 (v3,ver3)，此时旧修复 (v2,ver2) 仍在途中")
	})
	sim.After(35, func() {
		fmt.Printf("  t=35: 旧修复已到达，副本1 = %s（保留新版本，拒绝旧修复）\n", reps[1].Get("k"))
	})
	sim.Run()
	fmt.Printf("  副本1 拒绝旧修复次数 = %d\n", reps[1].RejectedRepairs)

	c.Read("k", nil) // 再读一次：全集群收敛到 v3
	sim.Run()
	fmt.Println("  再次读后收敛:")
	printStates(reps, "k")
}

func scenarioCancel() {
	fmt.Println("--- 场景3: 取消读不丢已承诺修复 ---")
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1)
	reps := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	rm := NewRepairManager(sim, net, reps, 4, 3, 50)
	c := NewCoordinator(sim, net, reps, rm, 1000)

	for _, r := range reps {
		r.Apply("k", VersionedValue{"v1", 1})
	}
	reps[0].Apply("k", VersionedValue{"v2", 2})

	h := c.Read("k", nil)
	sim.After(25, func() { // t=20 应答到齐、修复已承诺；t=25 取消读
		h.Cancel()
		fmt.Println("  t=25: 读被取消（修复承诺已于 t=20 进入管理器）")
	})
	sim.Run()
	fmt.Printf("  取消后修复仍完成，结果: %+v\n", rm.Outcomes)
	printStates(reps, "k")

	// 对照：应答到达前取消，则不产生任何承诺
	reps2 := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	sim2, net2 := NewSim(), (*Network)(nil)
	net2 = NewNetwork(sim2, 10, 10, 1)
	rm2 := NewRepairManager(sim2, net2, reps2, 4, 3, 50)
	c2 := NewCoordinator(sim2, net2, reps2, rm2, 1000)
	for _, r := range reps2 {
		r.Apply("k", VersionedValue{"v1", 1})
	}
	reps2[0].Apply("k", VersionedValue{"v2", 2})
	h2 := c2.Read("k", nil)
	sim2.After(5, func() { h2.Cancel() }) // t=5 取消，应答 t=20 才到
	sim2.Run()
	fmt.Printf("  对照（应答前取消）: 修复 RPC = %d，副本1 = %s（未修复，符合预期）\n",
		net2.Sent[MsgRepairReq], reps2[1].Get("k"))
}

func scenarioConcurrencyLimit() {
	fmt.Println("--- 场景4: 全局并发上限 ---")
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1)
	reps := []*Replica{NewReplica(0), NewReplica(1)}
	rm := NewRepairManager(sim, net, reps, 2, 3, 50) // 全局最多 2 个在途修复

	for k := 0; k < 6; k++ {
		key := fmt.Sprintf("key%d", k)
		reps[0].Apply(key, VersionedValue{"new", 2})
		reps[1].Apply(key, VersionedValue{"old", 1})
		rm.Schedule(key, 1, VersionedValue{"new", 2})
	}
	fmt.Printf("  6 个键的修复排队，全局上限=2，调度后瞬间在途=%d\n", rm.Inflight())
	sim.Run()
	fmt.Printf("  运行期最大在途观测值 = %d（从未超过上限），完成修复 = %d 个\n",
		rm.MaxInflightObserved, len(rm.Outcomes))
}

func scenarioLiveness() {
	fmt.Println("--- 场景5: 故障下重试有界，故障恢复后收敛（活性）---")
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1)
	reps := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	rm := NewRepairManager(sim, net, reps, 4, 3, 50)
	c := NewCoordinator(sim, net, reps, rm, 1000)

	for _, r := range reps {
		r.Apply("k", VersionedValue{"v1", 1})
	}
	reps[0].Apply("k", VersionedValue{"v2", 2})

	dropsLeft := 2 // 前 2 个修复请求被丢弃，之后网络恢复
	net.DropHook = func(kind MsgKind, from, to int) bool {
		if kind == MsgRepairReq && dropsLeft > 0 {
			dropsLeft--
			return true
		}
		return false
	}
	c.Read("k", nil)
	sim.Run()
	fmt.Printf("  前 2 次修复被丢弃后重试成功: %+v\n", rm.Outcomes)
	printStates(reps, "k")

	// 对照：网络持续故障 -> 重试有界，明确失败而非死循环
	sim2 := NewSim()
	net2 := NewNetwork(sim2, 10, 10, 1)
	reps2 := []*Replica{NewReplica(0), NewReplica(1)}
	rm2 := NewRepairManager(sim2, net2, reps2, 4, 3, 50)
	c2 := NewCoordinator(sim2, net2, reps2, rm2, 1000)
	reps2[0].Apply("k", VersionedValue{"v2", 2})
	reps2[1].Apply("k", VersionedValue{"v1", 1})
	net2.DropHook = func(kind MsgKind, from, to int) bool { return kind == MsgRepairReq }
	c2.Read("k", nil)
	sim2.Run()
	fmt.Printf("  持续故障: 修复 RPC = %d（上限 3 次），结果 = %+v，事件队列剩余 = %d（有界终止）\n",
		net2.Sent[MsgRepairReq], rm2.Outcomes, sim2.Pending())
}

func main() {
	fmt.Println("=== 读修复演示：单进程事件模拟器（虚拟时间 + 故障注入）===")
	scenarioMerge()
	fmt.Println()
	scenarioSafety()
	fmt.Println()
	scenarioCancel()
	fmt.Println()
	scenarioConcurrencyLimit()
	fmt.Println()
	scenarioLiveness()
}
