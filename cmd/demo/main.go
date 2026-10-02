// 运行样例：会话超时可撤销合并窗口（gap=5, maxLateness=10）。
package main

import (
	"fmt"

	"sessionwin"
)

func printChanges(step string, changes []session.Change) {
	fmt.Printf("%s\n", step)
	if len(changes) == 0 {
		fmt.Println("  (无变更)")
	}
	for _, c := range changes {
		fmt.Printf("  %-7s [%d, %d]\n", c.Type, c.Window.Start, c.Window.End)
	}
	fmt.Println()
}

func main() {
	m := session.NewMerger(5, 10)

	printChanges("1. 事件 t=0 到达:", m.Add(0))
	printChanges("2. 事件 t=9 到达（间隔 9 > gap=5，独立窗口）:", m.Add(9))
	printChanges("3. 迟到事件 t=4 到达（连接两个窗口）:", m.Add(4))
	printChanges("4. 删除事件 t=4（窗口重新分裂）:", m.Remove(4))
	printChanges("5. 水位线推进到 25（丢弃超出最大延迟的状态）:", m.AdvanceWatermark(25))

	fmt.Printf("当前水位线: %d, 保留事件状态数: %d, 当前窗口数: %d\n",
		m.Watermark(), m.EventCount(), len(m.Windows()))
}
