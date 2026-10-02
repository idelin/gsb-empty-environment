package main

import "fmt"

// 演示：3 节点集群（A/B/C）进行 4 轮全互联广播，
// 随后节点 A 依据矩阵时钟计算 GC 水位，验证 LSN=10 之前的事件
// 已被集群内每个节点完整接收，并触发物理日志截断。
func main() {
	cluster := NewCluster("A", "B", "C")

	for round := 1; round <= 4; round++ {
		cluster.BroadcastRound(fmt.Sprintf("round%d", round))
	}

	a := cluster.Node("A")
	fmt.Printf("通信结束后，节点 A 的日志共 %d 条（LSN 1..%d）\n\n", len(a.log), a.nextLSN)

	fmt.Println("节点 A 的矩阵时钟（行=被认知的节点，列=逻辑时间分量）：")
	fmt.Print(a.clock.String())

	cut := a.GCCut()
	fmt.Printf("\nGC 水位 MinVector（每个来源节点已被全网确认的最大 Seq）：A=%d B=%d C=%d\n\n",
		cut["A"], cut["B"], cut["C"])

	const checkLSN = 10
	allAcked := true
	for _, e := range a.log {
		if e.LSN < checkLSN && !a.Acknowledged(e) {
			allAcked = false
			break
		}
	}
	fmt.Printf("判定：LSN<%d 的所有事件均已被集群内每个节点接收并处理？ %v\n", checkLSN, allAcked)
	if !allAcked {
		fmt.Println("不满足截断条件，放弃 GC。")
		return
	}

	horizon, removed := a.TruncatePrefix()
	fmt.Printf("触发物理日志截断：回收 LSN 1..%d 共 %d 条，剩余 %d 条\n\n", horizon, len(removed), len(a.log))

	fmt.Println("被回收的历史消息：")
	for _, e := range removed {
		fmt.Printf("  LSN=%-2d origin=%s seq=%-2d data=%s\n", e.LSN, e.Origin, e.Seq, e.Data)
	}
	fmt.Println("截断后保留的日志：")
	for _, e := range a.log {
		fmt.Printf("  LSN=%-2d origin=%s seq=%-2d data=%s\n", e.LSN, e.Origin, e.Seq, e.Data)
	}
}
