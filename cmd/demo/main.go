// demo 演示 Multi-Paxos 集群的完整生命周期：
// 选举与租约提交 -> 网络分区 -> 新 Leader 补洞 -> 分区愈合与脏日志覆写。
package main

import (
	"fmt"
	"time"

	"multipaxos/paxos"
)

func drain(n *paxos.Node) []string {
	var out []string
	for {
		e, ok := n.Applied().Dequeue()
		if !ok {
			return out
		}
		out = append(out, fmt.Sprintf("slot%d=%q", e.Slot, string(e.Value)))
	}
}

func waitApplied(n *paxos.Node, count int) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n.CommitFrontier() >= uint64(count) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	nw := paxos.NewNetwork(5)
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(i, nw, 5*time.Second)
		nw.Register(nodes[i])
	}
	defer func() {
		for _, n := range nodes {
			n.Close()
		}
	}()

	fmt.Println("== 阶段 1: 节点 0 竞选并提交 a,b,c ==")
	for _, v := range []string{"a", "b", "c"} {
		slot, err := nodes[0].Propose([]byte(v))
		fmt.Printf("  node0 propose %q -> slot %d, err=%v\n", v, slot, err)
	}
	waitApplied(nodes[4], 3)
	fmt.Printf("  node4 applied: %v\n", drain(nodes[4]))
	fmt.Printf("  租约内连续提交, Prepare 消息数 = %d (仅首次竞选)\n", nw.Prepares.Load())

	fmt.Println("== 阶段 2: 网络分区 {0,1} | {2,3,4} ==")
	nw.Partition([]int{0, 1}, []int{2, 3, 4})
	_, err := nodes[0].Propose([]byte("dirty"))
	fmt.Printf("  旧 Leader node0 propose \"dirty\" -> err=%v (少数派无法达到多数派, 留下脏日志)\n", err)

	fmt.Println("== 阶段 3: 多数派选出新 Leader node2, Phase 1 补洞后提交 x,y ==")
	slot, err := nodes[2].Propose([]byte("x"))
	fmt.Printf("  node2 propose \"x\" -> slot %d, err=%v\n", slot, err)
	prepares := nw.Prepares.Load()
	slot, err = nodes[2].Propose([]byte("y"))
	fmt.Printf("  node2 propose \"y\" -> slot %d, err=%v (租约绕过 Phase 1, Prepare 未增加: %v)\n",
		slot, err, nw.Prepares.Load() == prepares)
	waitApplied(nodes[3], 5)
	fmt.Printf("  node3 applied: %v\n", drain(nodes[3]))

	fmt.Println("== 阶段 4: 分区愈合, 新 Leader 同步覆写少数派脏日志 ==")
	nw.Heal()
	nodes[2].SyncTo(0)
	nodes[2].SyncTo(1)
	waitApplied(nodes[0], 5)
	waitApplied(nodes[1], 5)
	fmt.Printf("  node0 applied: %v (脏值 \"dirty\" 已被安全覆写)\n", drain(nodes[0]))
	fmt.Printf("  node1 applied: %v\n", drain(nodes[1]))
	fmt.Printf("  旧 Leader 是否仍持有租约: %v (已被取代)\n", nodes[0].HasLease())
}
