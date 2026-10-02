// 运行样例：验收边界场景的完整演示。
//
// 交易在业务时间 10:00（Asia/Shanghai）发生；
// 10:05 收到 10:00 生效的汇率 A(7.20)（延迟发布）；
// 10:10 收到对 10:00 牌价的紧急更正 B(7.25)。
// 分别以知悉时间 10:06 与 10:11 查询，展示折算汇率的版本跃迁。
package main

import (
	"fmt"
	"time"

	"temporalfx"
)

var shanghai = time.FixedZone("Asia/Shanghai", 8*3600)

func at(v string) time.Time {
	t, err := temporalfx.ParseIn(shanghai, v)
	if err != nil {
		panic(err)
	}
	return t
}

func main() {
	s := temporalfx.NewStore()

	txn := at("2026-10-02 10:00")
	a, _ := s.Publish("USD/CNY", txn, at("2026-10-02 10:05"), "7.20", temporalfx.Initial, "10:00牌价A，延迟5分钟发布")
	b, _ := s.Publish("USD/CNY", txn, at("2026-10-02 10:10"), "7.25", temporalfx.Correction, "紧急更正10:00牌价B")

	fmt.Println("== 汇率发布日志 ==")
	for _, v := range s.Versions("USD/CNY") {
		fmt.Printf("  版本 %-10s 生效 %s 知悉 %s 牌价 %-7s 类型 %s 备注 %s\n",
			v.VersionID(), v.Effective.In(shanghai).Format("15:04"),
			v.Known.In(shanghai).Format("15:04"), temporalfx.Decimal(v.Value, 4), v.Kind, v.Note)
	}

	fmt.Println("\n== 版本跃迁：同一笔 10:00 交易（1000 USD），不同知悉时点查询 ==")
	for _, asOf := range []string{"2026-10-02 10:06", "2026-10-02 10:11"} {
		c, err := s.Convert("USD/CNY", "1000", txn, at(asOf))
		if err != nil {
			fmt.Printf("  知悉 %s 查询失败: %v\n", asOf[11:], err)
			continue
		}
		fmt.Printf("  知悉 %s 查询 -> 命中版本 %-10s 牌价 %-7s 本币 %s CNY\n",
			asOf[11:], c.Rate.VersionID(), temporalfx.Decimal(c.Rate.Value, 4),
			temporalfx.Decimal(c.LocalAmount, 2))
	}

	fmt.Println("\n== 审计日志（折算依据留痕） ==")
	for i, c := range s.AuditLog() {
		fmt.Printf("  [%d] txn=%s asOf=%s 依据: %s\n", i+1,
			c.TxnTime.In(shanghai).Format("15:04"), c.AsOf.In(shanghai).Format("15:04"), c.Basis)
	}
	_ = a
	_ = b
}
