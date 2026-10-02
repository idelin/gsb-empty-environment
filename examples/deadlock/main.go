// 运行样例：用单步调试模式定位“等待未初始化资源”导致的假死。
//
// 场景：worker 等待 initializer 通过 "config" 通道下发配置，
// 但 initializer 存在缺陷（直接返回），worker 永远阻塞。
// 调试器逐个 Tick 推动调度器，最后打印状态转换字典与假死报告。
package main

import (
	"fmt"
	"sort"

	"fiberdbg"
)

func main() {
	sch := fiberdbg.NewScheduler()
	config := fiberdbg.NewChannel("config", 0)

	sch.Spawn("initializer", func(f *fiberdbg.Fiber) {
		f.Sleep(2)
		// BUG: 缺少 f.Send(config, cfg)，资源永远未初始化
	})
	sch.Spawn("worker", func(f *fiberdbg.Fiber) {
		f.Recv(config)
		for {
			f.Yield()
		}
	})

	dbg := fiberdbg.NewDebugger(sch)

	fmt.Println("== 单步调试 ==")
	trace := func(action string) {
		fmt.Printf("tick=%d | %-14s |", sch.Clock(), action)
		for _, fs := range sch.Snapshot() {
			fmt.Printf("  %s:%s", fs.Name, fs.State)
		}
		fmt.Println()
	}

	dbg.Step()
	trace("step initializer")
	dbg.Step()
	trace("step worker")
	for sch.Clock() < 2 {
		dbg.Tick()
		trace("tick")
	}
	dbg.Step()
	trace("step initializer")

	fmt.Println("\n== 状态转换字典 ==")
	dict := sch.TransitionMap()
	ids := make([]int, 0, len(dict))
	for id := range dict {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		for _, tr := range dict[id] {
			fmt.Println(" ", tr)
		}
	}

	fmt.Println("\n== 假死报告 ==")
	for _, fs := range sch.Stuck() {
		fmt.Println(" ", fs)
	}
}
