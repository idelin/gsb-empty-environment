// demo 模拟向系统注入 100 个批处理任务：
// 内存监控器在 80% 阈值触发反压、拦截新协程创建，
// 存量协程释放内存后平滑恢复接单。
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"memsched"
)

func main() {
	const (
		budget = int64(1000) // 全局内存配额（字节单位示意）
		tasks  = 100
	)
	mon := memsched.NewMonitor(budget, 0.8, 0.6)
	sched := memsched.NewScheduler(mon, 32, 128)
	sched.Start()
	defer sched.Stop()

	rng := rand.New(rand.NewSource(42))
	start := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < tasks; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			size := int64(50 + rng.Intn(100)) // 每个任务 50~149 字节临时对象
			// 每 10 个任务安插一个具备 GC 回收能力的协程（优先调度）。
			reclaim := id%10 == 0
			f := sched.Submit(func(c *memsched.Context) error {
				c.Alloc(size)
				time.Sleep(time.Duration(1+rng.Intn(4)) * time.Millisecond) // 模拟批处理
				c.Release(size)
				return nil
			}, reclaim)
			if err := f.Wait(); err != nil {
				fmt.Printf("task %d failed: %v\n", id, err)
			}
		}(i)
	}
	wg.Wait()

	st := mon.Stats()
	fmt.Println("==== 协程内存配额与 GC 协作调度器 · 运行样例 ====")
	fmt.Printf("注入批处理任务:   %d\n", tasks)
	fmt.Printf("完成协程数:       %d\n", sched.Completed())
	fmt.Printf("内存配额上限:     %d (高水位 %d = 80%%, 低水位 %d = 60%%)\n", st.Max, st.High, st.Low)
	fmt.Printf("内存峰值:         %d (未越配额: %v)\n", st.Peak, st.Peak <= st.Max)
	fmt.Printf("反压触发次数:     %d (达到 80%% 阈值，拦截新协程创建)\n", st.BackpressureEvents)
	fmt.Printf("平滑恢复次数:     %d (存量协程释放内存后恢复接单)\n", st.ResumeEvents)
	fmt.Printf("准入协程总数:     %d\n", st.Admitted)
	fmt.Printf("结束时内存占用:   %d (无泄漏: %v)\n", st.Used, st.Used == 0)
	fmt.Printf("总耗时:           %v\n", time.Since(start).Round(time.Millisecond))
}
