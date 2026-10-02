// 运行样例：演示验收边界中的版本跃迁。
package main

import (
	"fmt"
	"time"

	"fxconv"
)

func at(h, m int) time.Time {
	return time.Date(2026, 10, 2, h, m, 0, 0, time.Local)
}

func main() {
	s := fxconv.NewStore()

	// 交易在业务 10:00 发生（1000 USD -> CNY）。
	txn := at(10, 0)

	// 10:05 收到 10:00 生效的汇率 A；10:10 收到对 10:00 牌价的紧急更正 B。
	s.Publish("USD/CNY", 7.10, at(10, 0), at(10, 5), "10:00 牌价 A")
	s.Correct("USD/CNY", 7.12, at(10, 0), at(10, 10), "紧急更正 B：10:00 牌价录入有误")

	fmt.Println("== 验收场景：同一笔 10:00 交易，按不同知悉时刻查询 ==")
	for _, asOf := range []time.Time{at(10, 6), at(10, 11)} {
		c, err := s.Convert("USD/CNY", 1000, txn, asOf)
		if err != nil {
			fmt.Println("折算失败:", err)
			continue
		}
		fmt.Println(c)
	}

	fmt.Println()
	fmt.Println("== 审计：版本库完整历史（只追加，永不删除） ==")
	for _, v := range s.History() {
		fmt.Println(" ", v)
	}
}
