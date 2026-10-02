// cabac-demo 演示 CABAC 内核对结构化指令序列的压缩效果：
// 高频指令的有效编码位长随编码推进收敛到约 0.2 比特，
// 且整体码率大幅领先无上下文的静态模型。
package main

import (
	"fmt"

	"cabac"
)

func main() {
	const n = 200000
	trace := cabac.GenerateTrace(n, 2024)

	nops := 0
	for _, ins := range trace {
		if ins.Op == cabac.OpNop {
			nops++
		}
	}
	fmt.Printf("指令序列: %d 条（高频指令 NOP 占比 %.1f%%，马尔可夫热区结构）\n\n",
		n, 100*float64(nops)/n)

	// 分段编码，观察有效位长随编码推进的收敛过程
	enc := cabac.NewEncoder()
	ie := cabac.NewInstrEncoder(enc, &cabac.ContextSet{})
	checkpoints := []int{1000, 2000, 5000, 10000, 20000, 50000, 100000, n}
	fmt.Println("收敛过程（有效编码位长）:")
	prev, prevBits := 0, 0
	for _, cp := range checkpoints {
		for _, ins := range trace[prev:cp] {
			ie.Encode(ins)
		}
		fmt.Printf("  第 %6d - %6d 条: %.3f 比特/指令\n",
			prev+1, cp, float64(enc.BitsWritten()-prevBits)/float64(cp-prev))
		prev, prevBits = cp, enc.BitsWritten()
	}
	enc.Flush()
	cabacData := enc.Bytes()

	// 无上下文基线
	single := cabac.EncodeInstrsSingleContext(trace)
	static := cabac.EncodeInstrsStatic(trace)
	fixed := cabac.FixedLengthBits(n) / 8

	fmt.Println("\n整体压缩率对比:")
	fmt.Printf("  %-28s %7d 字节  (%.3f 比特/指令)\n", "CABAC 上下文自适应", len(cabacData), float64(len(cabacData)*8)/n)
	fmt.Printf("  %-28s %7d 字节  (%.3f 比特/指令)\n", "单上下文自适应模型", len(single), float64(len(single)*8)/n)
	fmt.Printf("  %-28s %7d 字节  (%.3f 比特/指令)\n", "静态 0.5 概率模型", len(static), float64(len(static)*8)/n)
	fmt.Printf("  %-28s %7d 字节  (%.3f 比特/指令)\n", "定长编码", fixed, float64(fixed*8)/n)
	fmt.Printf("\nCABAC 相对节省: 单上下文 %.1f%% | 静态模型 %.1f%% | 定长 %.1f%%\n",
		100*(1-float64(len(cabacData))/float64(len(single))),
		100*(1-float64(len(cabacData))/float64(len(static))),
		100*(1-float64(len(cabacData))/float64(fixed)))

	// 往返校验
	got := cabac.DecodeTrace(cabacData, n)
	ok := len(got) == len(trace)
	for i := range got {
		if got[i] != trace[i] {
			ok = false
			break
		}
	}
	fmt.Printf("\n解码往返校验: %v\n", map[bool]string{true: "OK（完全一致）", false: "失败"}[ok])
}
