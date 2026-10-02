// demo 演示断点续传位点管理与崩溃安全恢复：
// 10 个工作线程并行消费位点 1000..2000，线程 3 卡在 1050，
// 模拟崩溃重启后，进度读取器返回 1050 作为安全重放起始点。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"checkpoint/checkpoint"
)

const (
	start   = 1000
	end     = 2000 // 闭区间
	workers = 10
	stuckAt = 1050
)

func main() {
	dir, err := os.MkdirTemp("", "checkpoint-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.dat")

	fmt.Printf("=== 阶段 1：%d 个工作线程并行消费位点 %d..%d ===\n", workers, start, end)
	mgr, err := checkpoint.Open(path, start)
	if err != nil {
		log.Fatal(err)
	}

	jobs := make(chan uint64)
	stuckJob := make(chan uint64, 1)
	go func() {
		for pos := uint64(start); pos <= end; pos++ {
			if pos == stuckAt {
				stuckJob <- pos // 1050 固定派给线程 3
				continue
			}
			jobs <- pos
		}
	}()

	stuckGate := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int, m *checkpoint.Manager) {
			defer wg.Done()
			if id == 3 {
				pos := <-stuckJob
				fmt.Printf("[线程 3] 领到位点 %d，处理较慢，卡住中...\n", pos)
				<-stuckGate
				m.Ack(pos)
				fmt.Printf("[线程 3] 位点 %d 终于处理完成\n", pos)
			}
			for pos := range jobs {
				m.Ack(pos) // 模拟消费完成
			}
		}(w, mgr)
	}

	for mgr.LowWatermark() != stuckAt {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	fmt.Printf("其余线程已消费完各自事件，低水位停在 %d（线程 3 的缺口）\n", mgr.LowWatermark())

	fmt.Println("\n=== 阶段 2：系统崩溃（不做任何清理，直接丢弃管理器）===")
	mgr = nil // 进程崩溃等价物：内存状态全部丢失，仅剩落盘水位

	fmt.Println("\n=== 阶段 3：重启，进度读取器恢复安全重放起始点 ===")
	resume, err := checkpoint.ReadCheckpoint(path, start)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("进度读取器返回安全重放起始点: %d（不漏掉任何未完成事件）\n", resume)

	mgr2, err := checkpoint.Open(path, start)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("从位点 %d 平滑续跑，重放 [%d, %d]...\n", resume, resume, end)
	for pos := resume; pos <= end; pos++ {
		mgr2.Ack(pos)
	}
	fmt.Printf("续跑完成，最终低水位: %d\n", mgr2.LowWatermark())

	close(stuckGate)
	close(jobs)
	wg.Wait()
}
