// demo 演示 5 节点集群在 2/3 对称分区下的多数派租约仲裁行为。
package main

import (
	"fmt"
	"time"

	"quorumlease"
)

const ttl = 300 * time.Millisecond

func status(c *lease.Cluster) string {
	s := ""
	for _, n := range c.Nodes() {
		mark := "-"
		if n.IsLeader() {
			mark = "L"
		}
		s += fmt.Sprintf("[%d:%s] ", n.ID(), mark)
	}
	return s
}

func tryWrite(c *lease.Cluster, id lease.NodeID) string {
	if err := c.Node(id).Write("key", "value"); err != nil {
		return "REJECTED"
	}
	return "accepted"
}

func main() {
	c := lease.NewCluster(5)
	defer c.Shutdown()

	fmt.Printf("集群: 5 节点, Quorum = %d, 租约 TTL = %v\n\n", c.Quorum(), ttl)

	fmt.Println("== 阶段 1: 全集群选举 node-0 为 Leader ==")
	if !c.Node(0).Campaign(ttl) {
		fmt.Println("node-0 竞选失败")
		return
	}
	fmt.Printf("状态: %s\n", status(c))
	fmt.Printf("node-0 写请求: %s\n\n", tryWrite(c, 0))

	fmt.Println("== 阶段 2: 对称分区 {0,1} | {2,3,4} (旧 Leader 在少数派) ==")
	c.Partition(0, 1)

	// 等待旧 Leader 租约过期、多数派选出新 Leader。
	for i := 0; i < 12; i++ {
		time.Sleep(ttl / 2)
		fmt.Printf("t=%4dms 状态: %s | node-0 写: %s | node-3 写: %s\n",
			(i+1)*int(ttl/time.Millisecond)/2, status(c), tryWrite(c, 0), tryWrite(c, 3))
		if i == 2 {
			if c.Node(3).Campaign(ttl) {
				fmt.Println("         --> node-3 在多数派分区赢得选举 (3/5 授权)")
			}
		}
	}

	fmt.Println("\n== 阶段 3: 分区恢复 ==")
	c.Heal()
	time.Sleep(ttl)
	fmt.Printf("状态: %s\n", status(c))
	fmt.Printf("node-0 写请求: %s (旧 Leader 不复活)\n", tryWrite(c, 0))
	fmt.Printf("node-3 写请求: %s (多数派 Leader 持续服务)\n", tryWrite(c, 3))
}
