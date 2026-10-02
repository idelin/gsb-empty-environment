// 双时态合同履约状态机演示：验收边界场景。
package main

import (
	"fmt"

	"bitemporal"
)

func printSnap(c *contract.Contract, knowledge, business int64) {
	s := c.SnapshotAt(knowledge, business)
	fmt.Printf("知悉 %2d | 业务 %3d -> 状态: %s, 累计交付: %3d, 完成度: %.0f%%\n",
		s.Knowledge, s.Business, s.State, s.Delivered, s.Completion)
}

func main() {
	c, err := contract.NewContract(1, 100, 100)
	if err != nil {
		panic(err)
	}
	fmt.Println("合同业务期 [1, 100)，履约总量 100")

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(c.AdvanceKnowledge(10))
	id, err := c.RecordDelivery(50, 30)
	must(err)
	fmt.Printf("\n知悉10: 录入业务50交付30 (凭证#%d)\n", id)

	must(c.AdvanceKnowledge(20))
	must(c.VoidDelivery(id))
	newID, err := c.RecordDelivery(40, 60)
	must(err)
	fmt.Printf("知悉20: 作废凭证#%d, 补录业务40交付60 (凭证#%d)\n", id, newID)

	fmt.Println("\n-- 验收对比：同一业务时刻 60，不同知悉时刻 --")
	printSnap(c, 15, 60)
	printSnap(c, 25, 60)

	fmt.Println("\n-- 补交改变历史切片：业务时刻 45 --")
	printSnap(c, 15, 45)
	printSnap(c, 25, 45)

	fmt.Println("\n-- 业务时间轴上的状态演进（知悉25视角）--")
	for _, bt := range []int64{0, 1, 39, 40, 99, 100} {
		printSnap(c, 25, bt)
	}
}
