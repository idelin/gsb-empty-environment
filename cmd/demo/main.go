// demo 在单进程事件模拟器中演示会话版本令牌：
// 场景1 会话迁移后的读己之写；场景2 分区下读超时显式失败；场景3 重启后令牌继续有效。
package main

import (
	"fmt"

	"sessionkv"
)

func cluster(sim *sessionkv.Simulator, delay int64, ids ...string) map[string]*sessionkv.Node {
	nodes := map[string]*sessionkv.Node{}
	for _, id := range ids {
		nodes[id] = sessionkv.NewNode(sim, id)
	}
	for _, a := range ids {
		for _, b := range ids {
			if a != b {
				nodes[a].AddPeer(nodes[b])
				sim.SetLink(a, b, true, delay)
			}
		}
	}
	return nodes
}

func scenario1() {
	fmt.Println("== 场景1: 写在A成功后迁移到B读取（读己之写，禁止返回旧值）==")
	sim := sessionkv.NewSimulator()
	nodes := cluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	s := sessionkv.NewClient(sim, "alice")
	s.Write(B, "profile", "v0-old", 100)
	fmt.Printf("[t=%3d] 写 profile=v0-old @B 确认, 令牌=%s\n", sim.Now(), s.Token().Vector)

	s.Write(A, "profile", "v1-new", 100) // 依赖 {B:1}，等 B->A 复制到达后应用
	fmt.Printf("[t=%3d] 写 profile=v1-new @A 已提交（等待依赖）\n", sim.Now())

	var moved *sessionkv.Client
	sim.After(15, func() {
		fmt.Printf("[t=%3d] 写 v1-new @A 确认, 令牌=%s\n", sim.Now(), s.Token().Vector)
		moved = s.Migrate(sim)
		fmt.Printf("[t=%3d] 会话 alice 迁移到节点 B\n", sim.Now())
		moved.Read(B, "profile", 500)
		fmt.Printf("[t=%3d] 在 B 发起读（B 当前只有旧值，读挂起等待复制）\n", sim.Now())
	})
	sim.After(25, func() {
		rec := moved.Reads[len(moved.Reads)-1]
		fmt.Printf("[t=%3d] 读完成: value=%q version=%s (复制到达后返回新值)\n",
			sim.Now(), rec.Res.Value, rec.Res.Version)
	})
	sim.Run()
	fmt.Println()
}

func scenario2() {
	fmt.Println("== 场景2: 网络分区下读超时（显式标记未满足，不静默降级）==")
	sim := sessionkv.NewSimulator()
	nodes := cluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	seed := sessionkv.NewClient(sim, "seed")
	seed.Write(B, "config", "stale", 100)

	sim.SetLink("A", "B", false, 0) // 分区
	fmt.Printf("[t=%3d] 网络分区: A -/-> B\n", sim.Now())

	s := sessionkv.NewClient(sim, "bob")
	s.Write(A, "config", "fresh", 100)
	fmt.Printf("[t=%3d] 写 config=fresh @A 确认, 令牌=%s\n", sim.Now(), s.Token().Vector)

	r := s.Read(B, "config", 50)
	fmt.Printf("[t=%3d] 在 B 读 config（B 有旧值 stale，但不满足令牌，等待）\n", sim.Now())
	sim.After(60, func() {
		fmt.Printf("[t=%3d] 读超时: err=%v\n", sim.Now(), r.Err)
	})
	sim.Run()
	fmt.Println()
}

func scenario3() {
	fmt.Println("== 场景3: 节点重启后令牌继续有效（反熵补齐，不降级）==")
	sim := sessionkv.NewSimulator()
	nodes := cluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	s := sessionkv.NewClient(sim, "carol")
	s.Write(A, "doc", "v1", 500)
	fmt.Printf("[t=%3d] 写 doc=v1 @A 确认, 令牌=%s\n", sim.Now(), s.Token().Vector)

	sim.After(1, func() { B.Crash(); fmt.Printf("[t=%3d] 节点 B 崩溃\n", sim.Now()) })
	var r *sessionkv.ReadResult
	sim.After(2, func() {
		r = s.Read(B, "doc", 500)
		fmt.Printf("[t=%3d] 在 B 读 doc（B 宕机，读挂起）\n", sim.Now())
	})
	sim.After(5, func() { B.Restart(); fmt.Printf("[t=%3d] 节点 B 重启，发起反熵\n", sim.Now()) })
	sim.After(40, func() {
		fmt.Printf("[t=%3d] 读完成: value=%q version=%s（重启后令牌仍被满足）\n",
			sim.Now(), r.Value, r.Version)
	})
	sim.Run()
	fmt.Println()
}

func main() {
	scenario1()
	scenario2()
	scenario3()
}
