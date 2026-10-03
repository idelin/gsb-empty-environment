// 演示：依赖图 A1->B1->C1, D1->C1 的增量重算与循环引用拒绝。
package main

import (
	"fmt"

	"sheetengine"
)

func main() {
	e := sheetengine.New()

	e.SetValue("E1", 100)
	e.SetValue("A1", 1)
	must(e.SetFormula("D1", "E1 + 1"))
	must(e.SetFormula("B1", "A1 * 2"))
	must(e.SetFormula("C1", "B1 + D1"))

	fmt.Println("== 初始状态 ==")
	fmt.Printf("A1=%.0f B1=%.0f C1=%.0f D1=%.0f\n",
		e.GetValue("A1"), e.GetValue("B1"), e.GetValue("C1"), e.GetValue("D1"))

	fmt.Println("\n== 单点变更：A1 = 5 ==")
	e.SetValue("A1", 5)
	fmt.Printf("B1=%.0f C1=%.0f D1=%.0f (D1 保持不变)\n",
		e.GetValue("B1"), e.GetValue("C1"), e.GetValue("D1"))
	fmt.Printf("重算次数: B1=%d C1=%d D1=%d (仅 B1、C1 被重算)\n",
		e.RecalcCount("B1"), e.RecalcCount("C1"), e.RecalcCount("D1"))

	fmt.Println("\n== 尝试添加循环公式 A1 = C1 + 1 ==")
	if err := e.SetFormula("A1", "C1 + 1"); err != nil {
		fmt.Println("已拒绝:", err)
	}
	fmt.Printf("隔离检查: A1=%.0f C1=%.0f (图状态未被破坏)\n",
		e.GetValue("A1"), e.GetValue("C1"))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
