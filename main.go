package main

import "fmt"

// 验收场景：
//   合同业务期 [1, 100)，知悉 0 录入条款：生效=1，履行中=10，终止=100，合同总量=100。
//   知悉 10：补录业务 50 的交付 30。
//   知悉 20：作废该交付，补录业务 40 的交付 45。
//   对比 (业务 60, 知悉 15) 与 (业务 60, 知悉 25) 的状态与完成度。
func main() {
	c := NewContract(0, Terms{EffectiveFrom: 1, PerformStart: 10, End: 100, TotalAmount: 100})

	id, _ := c.RecordDelivery(10, 50, 30)
	fmt.Printf("知悉10: 录入交付 凭证#%d 业务时间=50 数量=30\n", id)

	_ = c.VoidDelivery(20, id)
	id2, _ := c.RecordDelivery(20, 40, 45)
	fmt.Printf("知悉20: 作废凭证#%d，录入交付 凭证#%d 业务时间=40 数量=45\n", id, id2)

	fmt.Println("\n双时态切片查询（业务60）：")
	for _, k := range []int64{15, 25} {
		fmt.Printf("  知悉%-3d 状态=%s 完成度=%.0f%% 有效凭证=%v\n",
			k, c.StateAt(60, k), c.CompletionAt(60, k), c.VouchersAsOf(k))
	}

	fmt.Println("\n同一知悉时间沿业务时间轴的状态演进（知悉25）：")
	for _, b := range []int64{0, 5, 35, 45, 60, 100} {
		fmt.Printf("  业务%-4d 状态=%s 完成度=%.0f%%\n", b, c.StateAt(b, 25), c.CompletionAt(b, 25))
	}

	fmt.Println("\n条款时态变更（知悉30 改量 100 -> 150）：")
	c.AmendTerms(30, Terms{EffectiveFrom: 1, PerformStart: 10, End: 100, TotalAmount: 150})
	for _, k := range []int64{25, 35} {
		fmt.Printf("  知悉%-3d 业务60 状态=%s 完成度=%.1f%%\n", k, c.StateAt(60, k), c.CompletionAt(60, k))
	}
}
