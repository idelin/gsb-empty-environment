package main

import "fmt"

// 运行样例：6 节点集群分区为两个子集群，各自独立演化成员表，
// 分区愈合瞬间通过 4 字节 CRC 摘要比对察觉不一致并触发对账。
func main() {
	sim := NewSim()
	for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
		sim.AddNode(NewNode(id))
	}

	// 公共初始成员视图
	for _, n := range sim.nodes {
		n.Upsert(Member{ID: "A", State: Alive, Version: 1})
		n.Upsert(Member{ID: "B", State: Alive, Version: 1})
		n.Upsert(Member{ID: "C", State: Alive, Version: 1})
		n.Upsert(Member{ID: "D", State: Alive, Version: 1})
		n.Upsert(Member{ID: "E", State: Alive, Version: 1})
		n.Upsert(Member{ID: "F", State: Alive, Version: 1})
	}

	// 网络分区：{A,B,C} | {D,E,F}
	sim.Partition([]string{"A", "B", "C"}, []string{"D", "E", "F"})

	// 分区期间两侧独立演化：
	// 左側认为 D 已死并加入新成员 G；右侧认为 A 已死并加入新成员 H。
	sim.Node("A").Upsert(Member{ID: "D", State: Dead, Version: 2})
	sim.Node("A").Upsert(Member{ID: "G", State: Alive, Version: 1})
	sim.Node("B").Upsert(Member{ID: "D", State: Dead, Version: 2})
	sim.Node("B").Upsert(Member{ID: "G", State: Alive, Version: 1})
	sim.Node("C").Upsert(Member{ID: "D", State: Dead, Version: 2})
	sim.Node("C").Upsert(Member{ID: "G", State: Alive, Version: 1})

	sim.Node("D").Upsert(Member{ID: "A", State: Dead, Version: 2})
	sim.Node("D").Upsert(Member{ID: "H", State: Alive, Version: 1})
	sim.Node("E").Upsert(Member{ID: "A", State: Dead, Version: 2})
	sim.Node("E").Upsert(Member{ID: "H", State: Alive, Version: 1})
	sim.Node("F").Upsert(Member{ID: "A", State: Dead, Version: 2})
	sim.Node("F").Upsert(Member{ID: "H", State: Alive, Version: 1})

	fmt.Printf("分区期间摘要: 左侧(A)=%08x 右侧(D)=%08x\n",
		sim.Node("A").Digest(), sim.Node("D").Digest())

	// 分区愈合
	sim.Heal()
	fmt.Println("分区愈合，开始周期探活...")

	rounds := sim.RunUntilConverged(5)
	fmt.Printf("收敛轮次: %d\n", rounds)
	fmt.Printf("统计: %s\n", sim)
	fmt.Printf("愈合后摘要: A=%08x D=%08x 一致=%v\n",
		sim.Node("A").Digest(), sim.Node("D").Digest(), sim.Converged())

	fmt.Println("节点 A 最终成员视图:")
	for _, m := range sim.Node("A").Members() {
		fmt.Printf("  %s state=%s version=%d\n", m.ID, m.State, m.Version)
	}
}
