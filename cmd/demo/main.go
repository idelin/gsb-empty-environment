// demo 复现两个节点毫秒级争抢主租约的竞态，并回放审计追踪。
package main

import (
	"fmt"
	"sync"
	"time"

	"leaseaudit"
)

func main() {
	tracker := leaseaudit.NewTracker(nil)
	// 仲裁耗时 8ms：构成竞态窗口；租约 TTL 150ms
	arb := leaseaudit.NewArbiter(tracker, "primary", 150*time.Millisecond, 8*time.Millisecond, nil)

	fmt.Println("=== 阶段 1: node-A 与 node-B 同时争抢主租约 ===")
	start := make(chan struct{})
	var wg sync.WaitGroup
	outcome := make(map[string]bool)
	var mu sync.Mutex
	for _, node := range []string{"node-A", "node-B"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			ok, _ := arb.Acquire(id)
			mu.Lock()
			outcome[id] = ok
			mu.Unlock()
		}(node)
	}
	close(start)
	wg.Wait()
	fmt.Printf("仲裁结果: node-A=%v node-B=%v\n\n", outcome["node-A"], outcome["node-B"])

	winner := "node-A"
	if outcome["node-B"] {
		winner = "node-B"
	}

	fmt.Println("=== 阶段 2: 获胜者续期，随后移交租约 ===")
	arb.Renew(winner)
	other := "node-B"
	if winner == "node-B" {
		other = "node-A"
	}
	arb.Handoff(winner, other)

	fmt.Println("=== 阶段 3: 等待 TTL 到期，仲裁器回收租约 ===")
	time.Sleep(160 * time.Millisecond)
	expired, _ := arb.CheckTimeout()
	fmt.Printf("超时回收: %v\n\n", expired)

	fmt.Println("=== 完整审计追踪回放（时间轴） ===")
	fmt.Print(leaseaudit.Render(leaseaudit.Replay(tracker, "primary")))

	fmt.Println("=== 竞态证据链：从 REJECTED 事件回溯因果祖先 ===")
	var rejectSeq uint64
	for _, e := range tracker.EventsFor("primary") {
		if e.Type == leaseaudit.EventRejected {
			rejectSeq = e.Seq
		}
	}
	fmt.Print(leaseaudit.Render(tracker.CausalChain(rejectSeq)))
}
