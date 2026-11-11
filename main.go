// 回滚网络对战演示：在带随机延迟与丢包的链路上运行双端模拟，
// 校验两端最终状态一致且与确定性 oracle 完全相同。
package main

import (
	"fmt"

	"example.org/rollback/scenario"
)

func main() {
	cfg := scenario.DefaultConfig()
	fmt.Printf("运行配置: 帧数=%d 回滚窗口=%d 哈希周期=%d 延迟=%d+[0,%d) 丢包=%.0f%% 种子=%d\n",
		cfg.Frames, cfg.Window, cfg.HashInterval, cfg.MinLatency, cfg.Jitter,
		cfg.LossRate*100, cfg.Seed)

	res, err := scenario.New(cfg).Run()

	fmt.Println("\n--- 两端统计 ---")
	for p := 0; p < 2; p++ {
		s := res.Stats[p]
		fmt.Printf("玩家%d: 帧=%d 模拟步数=%d 回滚=%d 重算帧=%d 预测=%d 暂停=%d 丢包=%d 收包=%d\n",
			p, res.FinalFrame, s.FramesSimulated, s.Rollbacks, s.Resimulated,
			s.Predictions, s.Pauses, res.LinkLost[p], res.LinkDeliver[p])
	}

	fmt.Println("\n--- 一致性校验 ---")
	fmt.Printf("两端状态一致: %v\n", res.StatesEqual)
	fmt.Printf("与 oracle 一致: %v\n", res.HashMatch)
	fmt.Printf("不同步事件数: %d\n", len(res.Desyncs))
	fmt.Printf("最终哈希: %x\n", res.FinalHash[:8])

	if err != nil {
		fmt.Printf("\n校验失败: %v\n", err)
		return
	}
	fmt.Println("\n结论: 在随机延迟与丢包下，两端最终状态逐帧一致。")
}
