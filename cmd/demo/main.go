// 多币种联合授信双时态敞口监控器 —— 运行样例。
//
// 场景时间线（业务时间 / 记录时间）：
//
//	记录时间 0 : A 借款 600万 USD；USD 汇率 1.4；担保物 C1 估值 1200万；协议有效期 [0,50)
//	记录时间 5 : USD 汇率上调为 1.5（业务时间 15 起）
//	记录时间 20: 担保物 C1 估值下调为 800万（业务时间 20 起）
//	记录时间 30: 追溯上调 A 的历史借款为 700万（业务时间 0 起）
package main

import (
	"fmt"

	monitor "exposure-monitor"
)

func main() {
	s := monitor.NewStore(1000, 0.95, 1.10) // 集团授信 1000万，预警线 95%，质押率阈值 110%

	fmt.Println("== 记录时间 0：建立授信协议、借款、汇率、担保物 ==")
	s.RecordAgreement(0, 0, 50, monitor.Agreement{Subsidiary: "A", Limit: 1000})
	s.RecordLoan(0, 0, monitor.Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 600})
	s.RecordFxRate(0, 0, "USD", 1.4)
	s.RecordCollateral(0, 0, monitor.Collateral{ID: "C1", Subsidiary: "A", Value: 1200})

	fmt.Println("== 记录时间 5：USD 汇率波动 1.4 -> 1.5（业务时间 15 起）==")
	s.RecordFxRate(5, 15, "USD", 1.5)

	fmt.Println("== 记录时间 20：担保物估值下调 1200万 -> 800万（业务时间 20 起）==")
	s.RecordCollateral(20, 20, monitor.Collateral{ID: "C1", Subsidiary: "A", Value: 800})

	fmt.Println()
	fmt.Println("---- 汇率波动影响：同一笔借款在不同业务时间的折算 ----")
	fmt.Print(s.Replay(10, 29))
	fmt.Print(s.Replay(30, 29))

	fmt.Println("---- 担保物价值变动引发的质押率重算（业务时间 30，沿记录时间追溯）----")
	fmt.Print(s.Replay(30, 15)) // 估值下调尚未登记：质押率 75%
	fmt.Print(s.Replay(30, 25)) // 估值下调已登记：  质押率 112.5% -> 告警

	fmt.Println("== 记录时间 30：追溯上调 A 的历史借款 600万 -> 700万（业务时间 0 起）==")
	s.RecordLoan(30, 0, monitor.Loan{ID: "L1", Subsidiary: "A", Currency: "USD", Amount: 700})
	fmt.Println()

	fmt.Println("---- 修正前的历史视角（业务时间 30，记录时间 29）----")
	fmt.Print(s.Replay(30, 29))

	fmt.Println("---- 重现时间 30（记录时间 30）：总敞口触碰预警线 ----")
	fmt.Print(s.Replay(30, 30))

	fmt.Println("---- 授信协议过期后（业务时间 60，记录时间 30）----")
	fmt.Print(s.Replay(60, 30))
}
