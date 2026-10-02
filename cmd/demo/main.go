// demo 演示裂脑恢复合并：10 节点分裂为 [1-5] 与 [6-10] 两个分区，
// 各自独立演化后愈合网络，首个跨分区 PingReq 成功即触发合并握手，
// 最终全网收敛到同一视图。
package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"partitionmerge/merge"
)

func viewOf(clusterID uint64, ids []uint64) merge.View {
	members := make(map[uint64]merge.Member, len(ids))
	for _, id := range ids {
		members[id] = merge.Member{ID: id, State: merge.Alive, Incarnation: 1}
	}
	return merge.View{ClusterID: clusterID, Epoch: 1, Members: members}
}

func printState(title string, nodes []*merge.Node) {
	fmt.Println(title)
	for _, n := range nodes {
		v, clock := n.Snapshot()
		ids := make([]int, 0, len(v.Members))
		for id := range v.Members {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		fmt.Printf("  node %-2d cluster=%-20d epoch=%-2d clock=%-3d members=%v\n",
			n.ID, v.ClusterID, v.Epoch, clock, ids)
	}
}

func converged(nodes []*merge.Node, members int) (uint64, uint64, bool) {
	var cluster, epoch uint64
	for i, n := range nodes {
		v, _ := n.Snapshot()
		if len(v.Members) != members {
			return 0, 0, false
		}
		if i == 0 {
			cluster, epoch = v.ClusterID, v.Epoch
		} else if v.ClusterID != cluster || v.Epoch != epoch {
			return 0, 0, false
		}
	}
	return cluster, epoch, true
}

func main() {
	groupA := []uint64{1, 2, 3, 4, 5}
	groupB := []uint64{6, 7, 8, 9, 10}
	seeds := append(append([]uint64{}, groupA...), groupB...)

	net := merge.NewNetwork()
	var nodes []*merge.Node
	for _, id := range groupA {
		nodes = append(nodes, merge.NewNode(id, net, seeds, viewOf(1, groupA)))
	}
	for _, id := range groupB {
		nodes = append(nodes, merge.NewNode(id, net, seeds, viewOf(6, groupB)))
	}
	for _, n := range nodes {
		n.Start()
		defer n.Stop()
	}

	// 1. 网络分裂：两组各自独立演化。
	net.SetPartition(groupA, groupB)
	nodes[0].NoteLocalFailure(3) // A 组：node1 观测到 node3 疑似故障，epoch=2
	nodes[5].NoteLocalFailure(8) // B 组：node6 连续两次故障事件，epoch=3
	nodes[5].NoteLocalFailure(9)
	fmt.Println("=== phase 1: partition [1-5] | [6-10], evolving independently ===")
	time.Sleep(500 * time.Millisecond)
	printState("--- pre-merge views (diverged) ---", nodes)
	delivered, dropped := net.Stats()
	fmt.Printf("  network: delivered=%d dropped=%d (cross-partition traffic dropped)\n\n", delivered, dropped)

	// 2. 愈合网络：首个跨分区 PingReq 成功触发合并握手。
	fmt.Println("=== phase 2: network healed, merge handshake triggered ===")
	healedAt := time.Now()
	net.Heal()

	deadline := healedAt.Add(5 * time.Second)
	for {
		if cluster, epoch, ok := converged(nodes, 10); ok {
			fmt.Printf("converged in %v: cluster=%d epoch=%d members=10\n\n",
				time.Since(healedAt).Round(time.Millisecond), cluster, epoch)
			break
		}
		if time.Now().After(deadline) {
			printState("--- FAILED TO CONVERGE ---", nodes)
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 3. 合并后稳定运行，确认无死锁、无视图回退。
	time.Sleep(300 * time.Millisecond)
	printState("--- post-merge views (stable) ---", nodes)
	delivered, dropped = net.Stats()
	fmt.Printf("  network: delivered=%d dropped=%d\n", delivered, dropped)
	var rounds uint64
	for _, n := range nodes {
		rounds += n.MergeRounds()
	}
	fmt.Printf("  merge rounds initiated: %d\n", rounds)
	if _, _, ok := converged(nodes, 10); !ok {
		fmt.Println("views diverged after merge: FAILED")
		os.Exit(1)
	}
	fmt.Println("OK: split-brain healed, single merged cluster, no deadlock")
}
