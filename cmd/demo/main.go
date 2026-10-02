// Demo of the acceptance scenario: primary A receives a rolling-restart
// instruction, freezes local writes, hands the lease credential to candidate
// B, and B takes over seamlessly with zero write errors.
package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"leasectl/lease"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func main() {
	coord := lease.NewCoordinator(nil)
	nodeA := lease.NewNode("A", coord)
	nodeB := lease.NewNode("B", coord)
	router := lease.NewRouter(nodeA, nodeB)

	fmt.Println("=== 租约控制器演示:动态续期 + 平滑优雅移交 ===")

	// 1. A 获取租约,成为主节点。
	must(nodeA.Acquire("orders-service", 2*time.Second))
	fmt.Println("[1] 节点 A 获取租约 orders-service,TTL=2s,term=1")

	// 2. 动态续期:每次续期可使用不同的 TTL。
	for i := 1; i <= 3; i++ {
		time.Sleep(400 * time.Millisecond)
		must(nodeA.Renew(2 * time.Second))
		fmt.Printf("[2] A 第 %d 次动态续期成功,新到期时间 %s\n",
			i, nodeA.Credential().ExpiresAt.Format("15:04:05.000"))
	}
	must(nodeA.PutContext("checkpoint", "offset=1024"))
	fmt.Println("    A 写入租约上下文 checkpoint=offset=1024(移交时由 B 继承)")

	// 3. 业务流量持续写入当前主节点。
	ctx, cancel := context.WithCancel(context.Background())
	var writes, errs int64
	go func() {
		for ctx.Err() == nil {
			if err := router.Write(ctx, "order-event"); err == nil {
				atomic.AddInt64(&writes, 1)
			} else if ctx.Err() == nil {
				atomic.AddInt64(&errs, 1)
			}
			time.Sleep(100 * time.Microsecond) // 模拟业务写入节奏
		}
	}()
	time.Sleep(100 * time.Millisecond)
	fmt.Println("[3] 业务写入进行中(经由路由器发往当前主节点)...")

	// 4. A 收到滚动重启指令:冻结本地写入,向候选 B 移交租约凭证。
	fmt.Println("[4] A 收到滚动重启指令 -> 冻结本地写入,向候选 B 发起移交")
	switchover, err := nodeA.InitiateHandover(nodeB)
	must(err)
	cred := nodeB.Credential()
	fmt.Printf("    交接完成:B 无缝接管主角色,term=%d,剩余 TTL=%v\n",
		cred.Term, time.Until(cred.ExpiresAt).Round(time.Millisecond))
	fmt.Printf("    业务切换耗时 %v\n", switchover)

	// 5. B 继续服务并动态续期。
	time.Sleep(200 * time.Millisecond)
	must(nodeB.Renew(2 * time.Second))
	cancel()
	time.Sleep(5 * time.Millisecond) // 让写入循环退出

	fmt.Printf("[5] 结果:总写入 %d(A=%d, B=%d),错误 %d\n",
		atomic.LoadInt64(&writes), nodeA.Writes(), nodeB.Writes(), atomic.LoadInt64(&errs))
	fmt.Printf("    B 继承的租约上下文: %v\n", nodeB.Credential().Context)

	must(nodeB.Shutdown())
	if atomic.LoadInt64(&errs) == 0 {
		fmt.Println("=== 演示结束:全过程零错误中断 ===")
	} else {
		fmt.Println("=== 演示结束:出现写入错误 ===")
		os.Exit(1)
	}
}
