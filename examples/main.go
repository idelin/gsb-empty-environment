// 运行样例：演示 Batch 事务批处理的静默挂起与一次性级联刷新。
package main

import (
	"fmt"

	"reactive"
)

func main() {
	x := reactive.NewSignal(0)
	y := reactive.NewSignal(0)

	// 派生值：x + y（惰性求值）
	sum := reactive.NewComputed(func() interface{} {
		return x.Get().(int) + y.Get().(int)
	})

	// Effect 同时监听 X 和 Y（经由 sum 与直接读取）
	effectRuns := 0
	reactive.NewEffect(func() {
		effectRuns++
		fmt.Printf("[Effect] 第 %d 次触发: X=%d, Y=%d, X+Y=%d\n",
			effectRuns, x.Get().(int), y.Get().(int), sum.Get().(int))
	})

	fmt.Println("--- batch 外单独修改 X=1（立即触发） ---")
	x.Set(1)

	fmt.Println("--- 进入 batch：连续修改 X=10, Y=20 ---")
	reactive.Batch(func() {
		x.Set(10)
		y.Set(20)
		fmt.Printf("[Batch 内] 修改完成，Effect 触发次数仍为 %d（静默挂起）\n", effectRuns)
	})
	fmt.Printf("--- batch 退出，Effect 共触发 %d 次（一次性级联） ---\n", effectRuns)

	fmt.Println("--- 嵌套 batch ---")
	reactive.Batch(func() {
		x.Set(100)
		reactive.Batch(func() {
			y.Set(200)
		})
		fmt.Printf("[外层 batch 内] Effect 触发次数仍为 %d\n", effectRuns)
	})
	fmt.Printf("--- 最外层 batch 退出，Effect 共触发 %d 次 ---\n", effectRuns)
}
