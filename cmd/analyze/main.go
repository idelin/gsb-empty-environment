// Command analyze runs a consistency-hash quality diagnostic comparing
// MD5, MurmurHash3 and FNV-1a on a hash ring.
package main

import (
	"flag"
	"fmt"
	"strings"

	"hashring"
)

func main() {
	samples := flag.Int("samples", 2000000, "number of random keys to map")
	nodes := flag.Int("nodes", 10, "number of physical nodes")
	vnodes := flag.Int("vnodes", 160, "virtual nodes per physical node")
	seed := flag.Int64("seed", 42, "random seed")
	flag.Parse()

	nodeNames := make([]string, *nodes)
	for i := range nodeNames {
		nodeNames[i] = fmt.Sprintf("node-%02d", i)
	}

	fmt.Printf("=== 一致性哈希分布质量诊断报告 ===\n")
	fmt.Printf("节点数: %d | 虚拟节点: %d/节点 | 随机样本: %d | 种子: %d\n",
		*nodes, *vnodes, *samples, *seed)
	fmt.Printf("卡方检验的期望值按环上弧长份额计算，隔离虚拟节点布局随机性\n\n")

	fmt.Println("--- 随机键负载分布 ---")
	for _, nh := range hashring.DefaultHashes() {
		printReport(hashring.Analyze(nh.Name, nh.Func, nodeNames, *vnodes, *samples, *seed, hashring.RandomKeys))
	}

	fmt.Println("--- 顺序键负载分布（key-00000000 ...，低熵真实负载） ---")
	for _, nh := range hashring.DefaultHashes() {
		printReport(hashring.Analyze(nh.Name, nh.Func, nodeNames, *vnodes, *samples, *seed, hashring.SequentialKeys))
	}

	fmt.Println("=== 位宽-雪崩偏差曲线（低 w 位输出比特翻转概率与 0.5 的偏差）===")
	widths := []int{4, 8, 12, 16, 20, 24, 28, 32}
	for _, nh := range hashring.DefaultHashes() {
		fmt.Printf("\n[%s]\n", nh.Name)
		fmt.Printf("%6s %10s %10s %8s\n", "width", "meanDev", "maxDev", "badBits")
		for _, wp := range hashring.WidthCurve(nh.Func, 20000, widths, *seed) {
			fmt.Printf("%6d %10.4f %10.4f %8d\n", wp.Width, wp.MeanDev, wp.MaxDev, wp.BadBits)
		}
	}

	fmt.Println("\n=== 雪崩效应总览（理想：每个输出位翻转概率 ≈ 0.5）===")
	for _, nh := range hashring.DefaultHashes() {
		av := hashring.Avalanche(nh.Func, 20000, *seed)
		fmt.Printf("[%-20s] 平均偏差 %.4f | 最大偏差 %.4f (bit %d) | 异常位(|p-0.5|>0.1): %d/32\n",
			nh.Name, av.MeanDev, av.MaxDev, av.WorstBit, av.BadBits)
		var sb strings.Builder
		for j := 0; j < 32; j++ {
			mark := "."
			if av.BitProb[j]-0.5 > 0.1 || 0.5-av.BitProb[j] > 0.1 {
				mark = "X"
			}
			sb.WriteString(mark)
		}
		fmt.Printf("  bit0───────────────────────bit31: %s (X=异常)\n", sb.String())
	}
}

func printReport(r hashring.Report) {
	verdict := "均匀 (p>0.05，无法拒绝均匀分布假设)"
	if !r.Uniform {
		verdict = "不均匀 (p<=0.05，显著偏离均匀分布)"
	}
	fmt.Printf("[%s]\n", r.HashName)
	fmt.Printf("  均值负载:     %.1f\n", r.Mean)
	fmt.Printf("  标准差:       %.2f (变异系数 %.3f%%)\n", r.StdDev, r.CV*100)
	fmt.Printf("  最小/最大负载: %d / %d (最大最小比 %.4f)\n", r.Min, r.Max, r.MaxMinRatio)
	fmt.Printf("  卡方统计量:   %.2f (df=%d), p-value = %.4e\n", r.Chi2, r.DF, r.PValue)
	fmt.Printf("  拟合度结论:   %s\n\n", verdict)
}
