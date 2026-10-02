// demo 复现验收场景：
// 业务 A 租赁 80 个配额并在 10 秒后到期未归还；高优业务 B 此时申请 50 个配额；
// 协调器强行吊销 A 的超期租约并将配额转借给 B，输出配额违约惩罚审计日志。
package main

import (
	"context"
	"fmt"
	"time"

	"leasepool"
)

func main() {
	pool := leasepool.NewPool(100, 100*time.Millisecond) // 总配额 100，协调器每 100ms 巡检
	defer pool.Close()
	ctx := context.Background()

	fmt.Println("=== 租约配额池演示：总配额 100 ===")

	// 1. 业务 A 租赁 80 个配额，承诺 10 秒后归还。
	leaseA, err := pool.Acquire(ctx, "business-A", 80, 10*time.Second, leasepool.PriorityNormal)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[t=%5.1fs] 业务 A 租得 %d 个配额 (lease #%d)，承诺 %.0fs 后归还，当前空闲 %d\n",
		0.0, leaseA.Count, leaseA.ID, 10.0, pool.Available())

	// 2. 10 秒过去，A 到期未归还（模拟违约）。
	time.Sleep(10*time.Second + 500*time.Millisecond)
	fmt.Printf("[t=%5.1fs] 业务 A 已超期未归还！当前空闲配额 %d\n", 10.5, pool.Available())

	// 3. 高优业务 B 申请 50 个配额：空闲不足，协调器强行吊销 A 的超期租约并转借给 B。
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	leaseB, err := pool.Acquire(reqCtx, "business-B", 50, 30*time.Second, leasepool.PriorityHigh)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[t=%5.1fs] 高优业务 B 租得 %d 个配额 (lease #%d)，等待耗时 %v，当前空闲 %d\n",
		time.Since(start).Seconds()+10.5, leaseB.Count, leaseB.ID,
		time.Since(start).Round(time.Millisecond), pool.Available())
	fmt.Printf("           业务 A 的租约状态: revoked=%v active=%v\n", leaseA.Revoked(), leaseA.Active())

	// 4. 输出配额违约惩罚审计日志。
	fmt.Println("\n=== 配额审计日志 ===")
	for _, e := range pool.AuditLog() {
		fmt.Println(e)
	}
	fmt.Println("\n=== 违约惩罚积分 ===")
	for _, owner := range []string{"business-A", "business-B"} {
		fmt.Printf("%-12s penalty=%d\n", owner, pool.Penalty(owner))
	}
}
