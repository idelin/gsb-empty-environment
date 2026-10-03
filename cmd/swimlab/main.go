package main

import (
	"fmt"

	"swimlab"
)

// 运行验收场景：注入 10 次真实节点宕机与 5 次短暂网络丢包扰动，
// 由分析仪统计每个事件的发生时刻、首次怀疑时刻、最终标记时刻，
// 并输出 MTTD 与误报率的详尽诊断报告。
func main() {
	cfg, events, injections, periods := swimlab.AcceptanceScenario()

	sim := swimlab.NewSimulator(cfg)
	observations := sim.Run(periods, injections)

	analyzer := swimlab.NewAnalyzer()
	for _, e := range events {
		analyzer.RegisterEvent(e)
	}
	for _, o := range observations {
		analyzer.RecordObservation(o)
	}

	fmt.Print(analyzer.Analyze().String())
}
