// demo 模拟完整验收场景：
//  1. 源库 20 万行初始数据，全量同步到目标库；
//  2. 变更流持续产生（约 10 万条/秒）并经复制链路实时同步；
//  3. 巡检器每 50ms 比对两端校验和；
//  4. 运行中人为在目标库悄悄修改某行单个字段；
//  5. 巡检器在当前周期内捕捉分桶 Checksum 失配，并精确定位主键。
package main

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"checksumdiff"
)

const (
	totalRows    = 200_000
	scanInterval = 50 * time.Millisecond
)

func main() {
	src := checksumdiff.NewStore("source")
	dst := checksumdiff.NewStore("target")

	// 1. 初始全量同步
	for i := 0; i < totalRows; i++ {
		r := checksumdiff.Row{
			ID: int64(i), Name: fmt.Sprintf("user-%d", i),
			Balance: int64(i * 100), UpdatedAt: time.Now().UnixNano(), Version: 1,
		}
		src.Upsert(r)
		dst.Upsert(r)
	}
	fmt.Printf("[init] 全量同步完成: %d 行, src root=%016x dst root=%016x\n", totalRows, src.Root(), dst.Root())

	// 2. 变更生成器 + 复制链路
	changes := make(chan checksumdiff.Row, 1<<16)
	stop := make(chan struct{})
	var synced atomic.Int64
	go func() { // 源库持续产生变更
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		for {
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < 500; i++ {
				id := int64(rng.Intn(totalRows))
				r, _ := src.Get(id)
				r.Balance += int64(rng.Intn(1000))
				r.Version++
				r.UpdatedAt = time.Now().UnixNano()
				src.Upsert(r)
				changes <- r
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	go func() { // 复制链路：源 -> 目标
		for {
			select {
			case <-stop:
				return
			case r := <-changes:
				dst.Upsert(r)
				synced.Add(1)
			}
		}
	}()

	// 3. 巡检循环
	insp := checksumdiff.NewInspector(src, dst)
	suspects := map[int64]int{} // pk -> 连续出现次数（过滤复制延迟造成的瞬时差异）
	tampered := false
	var tamperID int64
	var tamperAt time.Time
	start := time.Now()
	scans := 0

	fmt.Printf("[scan] 巡检启动，周期 %s，变更流约 10 万条/秒\n", scanInterval)
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for range ticker.C {
		scans++
		rep := insp.Inspect()

		// 4. 运行约 3s 后（累计数十万条变更），人为篡改目标库某行单个字段（绕过复制链路）
		if !tampered && time.Since(start) > 3*time.Second {
			tamperID = int64(rand.New(rand.NewSource(99)).Intn(totalRows))
			row, _ := dst.Get(tamperID)
			row.Balance += 1 // 只改一个字段
			dst.Upsert(row)
			tampered = true
			tamperAt = time.Now()
			fmt.Printf("[tamper] 已悄悄篡改目标库 pk=%d 的 Balance 字段 (+1)\n", tamperID)
		}

		current := map[int64]bool{}
		for _, d := range rep.AllDiffs() {
			current[d.ID] = true
			suspects[d.ID]++
			if suspects[d.ID] >= 2 { // 连续两个周期仍存在 -> 真实不一致
				fmt.Printf("[ALERT] 数据不一致! pk=%d kind=%s src.Balance=%d dst.Balance=%d 检测延迟=%s\n",
					d.ID, d.Kind, d.Source.Balance, d.Target.Balance, time.Since(tamperAt).Round(time.Millisecond))
				if tampered && d.ID == tamperID {
					fmt.Printf("[done] 共同步 %d 条变更, 巡检 %d 个周期, 平均周期耗时 %s\n",
						synced.Load(), scans, rep.Duration)
					close(stop)
					return
				}
			}
		}
		for id := range suspects {
			if !current[id] {
				delete(suspects, id) // 瞬时差异已随复制收敛
			}
		}
		if scans%20 == 0 {
			fmt.Printf("[scan] 周期 #%d: 已同步 %d 条变更, 本周期比对耗时 %s, 状态=%s\n",
				scans, synced.Load(), rep.Duration, map[bool]string{true: "一致", false: "存在瞬时差异(复制延迟)"}[rep.Equal])
		}
	}
}
