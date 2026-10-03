// demo 用真实时钟演示 5 节点集群的多数派租约仲裁与脑裂预防：
// 选举 -> 分区 {1,2}|{3,4,5} -> 少数派续订失败、租约过期自动退位 ->
// 多数派重新选举并继续服务 -> 恢复分区。
package main

import (
	"fmt"
	"time"

	lease "lease-arbiter"
)

const leaseDuration = 2 * time.Second

func status(c *lease.Cluster) string {
	s := ""
	for i := 1; i <= c.Size(); i++ {
		if c.Node(i).IsLeader() {
			s += fmt.Sprintf(" [node%d:LEADER]", i)
		} else {
			s += fmt.Sprintf(" [node%d:-]", i)
		}
	}
	return s
}

func tryWrite(n *lease.Node, cmd string) {
	if err := n.Write(cmd); err != nil {
		fmt.Printf("  write %-22s -> node%d REJECTED: %v\n", cmd, n.ID(), err)
	} else {
		fmt.Printf("  write %-22s -> node%d ACCEPTED\n", cmd, n.ID())
	}
}

func main() {
	c := lease.NewCluster(5, leaseDuration, nil) // nil = 真实时钟

	fmt.Println("== 1. 全连通，node1 发起选举 ==")
	if err := c.Node(1).Elect(); err != nil {
		panic(err)
	}
	fmt.Println("  node1 获得 5/5 租约授权，成为 Leader")
	fmt.Println("  状态:" + status(c))
	tryWrite(c.Node(1), "set a=1")

	fmt.Println("\n== 2. 后台每 500ms 续订租约，随后发生分区 {1,2} | {3,4,5} ==")
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				c.Node(1).Renew() // 少数派分区中将持续失败
			}
		}
	}()
	c.Partition([]int{1, 2}, []int{3, 4, 5})
	fmt.Println("  已分区。node1 在 2 节点分区中续订只能拿到 2/5 授权 -> 失败")

	fmt.Println("\n== 3. 租约有效期内：多数派分区选举仍被授权互斥阻止 ==")
	time.Sleep(500 * time.Millisecond)
	if err := c.Node(4).Elect(); err != nil {
		fmt.Printf("  node4 选举失败（预期内）: %v\n", err)
	}
	fmt.Println("  状态:" + status(c))

	fmt.Println("\n== 4. 等待租约过期（2s）... ==")
	time.Sleep(leaseDuration)
	fmt.Println("  状态:" + status(c) + "  <- node1 租约过期，主权限自动剥离")
	tryWrite(c.Node(1), "set b=2")

	fmt.Println("\n== 5. 多数派分区重新选举 ==")
	if err := c.Node(4).Elect(); err != nil {
		panic(err)
	}
	fmt.Println("  node4 获得 3/5 租约授权，成为新 Leader")
	fmt.Println("  状态:" + status(c))
	tryWrite(c.Node(4), "set b=2")
	tryWrite(c.Node(1), "set c=3")

	fmt.Println("\n== 6. 恢复分区：旧 Leader 不会复活 ==")
	close(stop)
	c.Heal()
	time.Sleep(100 * time.Millisecond)
	fmt.Println("  状态:" + status(c))
	fmt.Printf("  node1 日志: %v（分区后未接受任何写）\n", c.Node(1).Log())
	fmt.Printf("  node4 日志: %v\n", c.Node(4).Log())
}
