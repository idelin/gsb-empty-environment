// demo 演示多副本库存模拟：分区 -> 离线扣减/额度转让 -> 重连合并。
package main

import (
	"fmt"

	inventory "inventorysim"
)

func printState(net *inventory.Network, total int64, reps ...*inventory.Replica) {
	fmt.Println("副本状态：")
	for _, r := range reps {
		fmt.Printf("  %-4s 持有额度 %4d   已扣减 %4d   可用 %4d\n",
			r.ID, r.Rights(), r.Spent(), r.Available())
	}
	fmt.Printf("  合计: 额度 %d + 在途 %d = %d (总量 %d)，已扣减 %d，剩余库存 %d\n",
		net.TotalRights(), net.InFlight(), net.TotalRights()+net.InFlight(),
		total, net.TotalSpent(), total-net.TotalSpent())
}

func printLedger(reps ...*inventory.Replica) {
	fmt.Println("转让流水（合并后各副本一致）：")
	for _, t := range reps[0].Ledger() {
		fmt.Printf("  %s\n", t)
	}
}

func main() {
	const total = 100
	a := inventory.NewReplica("A", 40)
	b := inventory.NewReplica("B", 30)
	c := inventory.NewReplica("C", 30)
	net := inventory.NewNetwork(a, b, c)

	fmt.Println("== 初始 ==")
	printState(net, total, a, b, c)

	fmt.Println("\n== 分区：{A,B} 与 {C} 隔离 ==")
	net.Partition([]string{"A", "B"})

	// A 热销，初始额度不够，向同组的 B 转让 20。
	a.Deduct(38)
	msg, err := b.Grant("A", 20)
	if err != nil {
		panic(err)
	}
	a.Receive(msg)
	a.Deduct(20)

	// C 离线独立扣减；尝试向 A 转让 10，但跨分区消息送不到。
	c.Deduct(20)
	lost, err := c.Grant("A", 10)
	if err != nil {
		panic(err)
	}
	net.Sync() // 分区中同步：跨组消息不可达
	fmt.Println("分区期间（C->A 的 10 额度在途，A 不可预支）：")
	printState(net, total, a, b, c)

	fmt.Println("\n== 重连合并 ==")
	net.Heal()
	a.Receive(lost) // 延迟消息送达（重复投递也只会授予一次）
	a.Receive(lost)
	net.Sync()
	printState(net, total, a, b, c)
	fmt.Println()
	printLedger(a, b, c)
}
