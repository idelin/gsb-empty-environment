// demo 演示双时态价格规则引擎的验收边界场景。
package main

import (
	"fmt"

	pricing "bitemporal-pricing"
)

func main() {
	var l pricing.Ledger

	// 价格 10 分：业务 [0,20) 有效，知悉于时刻 2。
	l.AddRule(0, 20, 10, 2)
	// 价格 15 分：业务 [5,10) 有效，知悉于时刻 8。
	l.AddRule(5, 10, 15, 8)

	fmt.Println("== 验收边界 ==")
	fmt.Printf("业务 7 @ 知悉 7 -> %d 分（新价尚不知悉）\n", l.MustPriceAt(7, 7))
	fmt.Printf("业务 7 @ 知悉 8 -> %d 分（后入为主）\n", l.MustPriceAt(7, 8))
	fmt.Printf("业务10 @ 知悉 8 -> %d 分（新价区间 [5,10) 不含 10）\n", l.MustPriceAt(10, 8))

	fmt.Println("== 撤销语义 ==")
	l.Retract(9) // 知悉时刻 9 撤销全部规则
	if _, err := l.PriceAt(7, 9); err != nil {
		fmt.Printf("业务 7 @ 知悉 9 -> %v（撤销已生效）\n", err)
	}
	fmt.Printf("业务 7 @ 知悉 8 -> %d 分（撤销不影响历史知悉查询）\n", l.MustPriceAt(7, 8))
}
