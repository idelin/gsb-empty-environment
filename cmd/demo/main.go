// demo 演示断点续传位点管理与崩溃安全恢复：
// 10 个工作线程并行消费位点 1000..2000，线程 3 卡在 1050；
// 模拟崩溃重启后，从检查点恢复并以 1050 为起点平滑续跑。
package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sync"

	"checkpoint"
)

const (
	base    = 1000
	end     = 2000 // 含
	workers = 10
	slow    = 3
	stuckAt = 1050
)

// chunkFor 返回线程 w 负责的位点区间 [lo, hi)。
// 线程 3 拿到第一块 [1000,1100)，因此位点 1050 由线程 3 负责。
func chunkFor(w int) (uint64, uint64) {
	chunkSize := uint64((end - base + 1) / workers)
	c := (w - slow + workers) % workers
	lo := uint64(base) + uint64(c)*chunkSize
	hi := lo + chunkSize
	if c == workers-1 {
		hi = end + 1
	}
	return lo, hi
}

func main() {
	dir, err := ioutil.TempDir("", "checkpoint-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.json")

	// ---- 第一阶段：并发消费，线程 3 卡在 1050 ----
	m, err := checkpoint.Open(path, base)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[启动] 位点范围 [%d, %d]，%d 个工作线程\n", base, end, workers)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		lo, hi := chunkFor(w)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pos := lo; pos < hi; pos++ {
				if !m.Begin(pos) {
					continue
				}
				if w == slow && pos >= stuckAt {
					fmt.Printf("[线程 %d] 处理缓慢，卡在位点 %d（已领取未完成）\n", w, pos)
					return // 悬挂：Begin 了但未 Commit
				}
				if err := m.Commit(pos); err != nil {
					log.Printf("线程 %d commit %d 失败: %v", w, pos, err)
					return
				}
			}
			fmt.Printf("[线程 %d] 完成 [%d, %d)\n", w, lo, hi)
		}()
	}
	wg.Wait()
	fmt.Printf("[崩溃前] Low Watermark = %d\n", m.LowWatermark())

	// ---- 模拟系统崩溃：不做任何清理，直接丢弃内存状态 ----
	fmt.Println("[崩溃] 系统宕机，内存状态丢失")
	m = nil

	// ---- 第二阶段：重启，读取安全重放起始点并续跑 ----
	resume, err := checkpoint.LoadResumePoint(path)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("[重启] 进度读取器返回安全重放起始点 = %d\n", resume)

	m2, err := checkpoint.Open(path, base)
	if err != nil {
		log.Fatal(err)
	}
	for pos := resume; pos <= end; pos++ {
		if !m2.Begin(pos) {
			continue
		}
		if err := m2.Commit(pos); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("[续跑完成] 从 %d 重放至 %d，最终 Low Watermark = %d\n",
		resume, end, m2.LowWatermark())
}
