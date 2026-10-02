package fiberdbg_test

import (
	"fmt"

	"fiberdbg"
)

// 单步调试一个因资源未初始化而假死的系统。
func Example() {
	sch := fiberdbg.NewScheduler()
	config := fiberdbg.NewChannel("config", 0)

	sch.Spawn("initializer", func(f *fiberdbg.Fiber) {
		f.Sleep(2)
		// BUG: 缺少 f.Send(config, cfg)
	})
	sch.Spawn("worker", func(f *fiberdbg.Fiber) {
		f.Recv(config)
	})

	dbg := fiberdbg.NewDebugger(sch)
	dbg.Step() // initializer 入睡
	dbg.Step() // worker 阻塞在 Recv
	dbg.Tick() // tick 1
	dbg.Tick() // tick 2，initializer 醒来
	dbg.Step() // initializer 结束，未初始化资源

	for _, tr := range sch.TransitionMap()[1] {
		fmt.Println(tr)
	}
	for _, fs := range sch.Stuck() {
		fmt.Println("STUCK:", fs)
	}

	// Output:
	// tick=0 fiber=1(worker) New -> Ready (spawned)
	// tick=0 fiber=1(worker) Ready -> Running (resumed)
	// tick=0 fiber=1(worker) Running -> WaitChanRecv (waiting to receive on channel 'config' (no sender))
	// STUCK: fiber=1(worker) state=WaitChanRecv instr=1 reason="waiting to receive on channel 'config' (no sender)"
}
