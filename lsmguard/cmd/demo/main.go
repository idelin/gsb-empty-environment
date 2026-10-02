// demo 复现验收场景：写入随机散列 Key → 关闭部分 Bloom Filter →
// 监控器即时捕捉读放大跃升并输出 Compaction 告警。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"lsmguard"
)

func randKey(i int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("workload-key-%d", i)))
	return hex.EncodeToString(sum[:])
}

func main() {
	const (
		numTables = 4
		tableSize = 250
		window    = 100
	)

	engine := lsmguard.NewEngine(tableSize)
	monitor := lsmguard.NewMonitor(window)
	monitor.SetOnAlert(func(a lsmguard.Alert) {
		fmt.Println("  🚨 监控告警:", a.String())
	})
	engine.SetOnGet(monitor.Observe)

	// 1. 写入随机散列 Key，形成 4 个 SSTable
	fmt.Printf("== 写入 %d 个随机散列 Key（每 %d 个 Flush 一次）==\n", numTables*tableSize, tableSize)
	keys := make([]string, 0, numTables*tableSize)
	for i := 0; i < numTables*tableSize; i++ {
		k := randKey(i)
		keys = append(keys, k)
		engine.Put(k, fmt.Sprintf("value-%d", i))
	}
	engine.Flush()
	fmt.Printf("   当前 SSTable 数量: %d\n\n", engine.NumSSTables())

	// 2. 阶段一：Bloom Filter 全部开启，正常读
	fmt.Println("== 阶段一：Bloom Filter 全部开启 ==")
	for i := 0; i < window; i++ {
		engine.Get(keys[i%len(keys)])
	}
	m := monitor.Metrics()
	fmt.Printf("   读放大 = %.2f | Bloom 误判率 = %.2f%%\n\n", m.ReadAmplification, m.FalsePositiveRate*100)

	// 3. 阶段二：故意关闭部分 Bloom Filter（模拟故障）
	fmt.Println("== 阶段二：故意关闭全部 4 个 SSTable 的 Bloom Filter ==")
	for i := 0; i < numTables; i++ {
		engine.DisableBloom(i)
	}
	monitor.ResetAlertCooldown()
	for i := 0; i < window; i++ {
		engine.Get(keys[i%len(keys)])
	}
	m = monitor.Metrics()
	fmt.Printf("   读放大 = %.2f | Bloom 误判率 = %.2f%%\n\n", m.ReadAmplification, m.FalsePositiveRate*100)

	fmt.Println("== 诊断结论 ==")
	fmt.Println("   读放大跃升 → 建议增加 Compaction 并发/权重，加速层级合并压制读延迟；")
	fmt.Println("   若误判率同步超标 → 建议重建 Bloom Filter。")
}
