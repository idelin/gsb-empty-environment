// demo 模拟验收场景：
//  1. 写入随机散列 Key，建立 5 个带 Bloom Filter 的 SSTable（健康基线）；
//  2. 故意关闭全部 Bloom Filter，读放大从 ~1.05 跃升至 4.2，监控器即时告警；
//  3. 用位数过小的过滤器重建，误判率飙升，监控器建议重建 Filter；
//  4. 执行 Compaction 并重建过滤器，读放大回落至 1.0。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"lsmwatch"
)

const (
	numTables   = 5
	keysPerTab  = 400
	healthyGets = 2000
)

func hashKey(prefix string, i int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", prefix, i)))
	return hex.EncodeToString(sum[:])
}

// batchKey 返回第 b 批（0=最旧）第 j 个 key。
func batchKey(b, j int) string { return hashKey("key", b*keysPerTab+j) }

func printSnapshot(tag string, snap lsmwatch.Snapshot) {
	fmt.Printf("【%s】累计Get=%d 累计读放大=%.3f 窗口读放大=%.3f 窗口误判率=%.2f%% 基线=%.3f\n",
		tag, snap.Gets, snap.ReadAmplification, snap.WindowReadAmplification,
		snap.WindowFalsePositiveRate*100, snap.BaselineReadAmplification)
}

func main() {
	mon := lsmwatch.NewMonitor(300)
	mon.OnAlert = func(a lsmwatch.Alert) { fmt.Printf("  🚨 告警: %s\n", a) }
	eng := lsmwatch.NewEngine(mon)

	fmt.Println("== 阶段1: 写入随机散列 Key，Bloom Filter 正常（健康基线） ==")
	for b := 0; b < numTables; b++ {
		for j := 0; j < keysPerTab; j++ {
			eng.Put(batchKey(b, j), "v")
		}
		eng.Flush()
	}
	fmt.Printf("已写入 %d 个 Key，共 %d 个 SSTable，Bloom Filter 8 bits/key\n",
		numTables*keysPerTab, len(eng.Tables()))
	for i := 0; i < healthyGets; i++ {
		eng.Get(batchKey(i%numTables, (i/numTables)%keysPerTab))
	}
	mon.Calibrate()
	printSnapshot("健康基线", mon.Snapshot())

	fmt.Println("\n== 阶段2: 故意关闭全部 Bloom Filter（故障注入） ==")
	eng.SetBloomEnabled(false)
	// 60% 未命中（读全部 5 表）+ 40% 命中（均匀分布，平均读 3 表）=> 读放大 4.2
	for i := 0; i < 1000; i++ {
		if i%10 < 6 {
			eng.Get(hashKey("ghost", i)) // 不存在的 Key
		} else {
			pos := (i / 10) % numTables // 命中表位置 0(最新)..4(最旧)
			eng.Get(batchKey(numTables-1-pos, (i/10/numTables)%keysPerTab))
		}
	}
	printSnapshot("Bloom 关闭后", mon.Snapshot())

	fmt.Println("\n== 阶段3: 重开 Bloom Filter，但位数过小（2 bits/key） ==")
	eng.SetBloomEnabled(true)
	eng.RebuildFilters(2)
	for i := 0; i < 500; i++ {
		eng.Get(batchKey(i%numTables, (i/numTables)%keysPerTab))
	}
	printSnapshot("过滤器过小", mon.Snapshot())

	fmt.Println("\n== 阶段4: 采纳建议 —— 重建 Filter(10 bits/key) + Compaction ==")
	eng.RebuildFilters(10)
	eng.Compact()
	for i := 0; i < 500; i++ {
		eng.Get(batchKey(i%numTables, (i/numTables)%keysPerTab))
	}
	printSnapshot("治理后", mon.Snapshot())

	fmt.Println("\n== 监控器输出的全部告警 ==")
	for _, a := range mon.Snapshot().Alerts {
		fmt.Println(" -", a)
	}
}
