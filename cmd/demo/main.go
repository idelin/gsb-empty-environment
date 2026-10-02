// demo 复现验收边界场景：
// 业务 A 租赁 80 个配额到期未归还 -> 协调器强制吊销 -> 高优业务 B 申请 50 个配额成功。
package main

import (
	"flag"
	"fmt"
	"time"

	"leasepool/lease"
)

func main() {
	ttl := flag.Duration("ttl", 10*time.Second, "业务 A 承诺借期")
	sweep := flag.Duration("sweep", 500*time.Millisecond, "协调器巡检周期")
	flag.Parse()

	pool := lease.NewPool(100, lease.WithAuditHook(func(e lease.AuditEvent) {
		fmt.Println("  AUDIT", e)
	}))
	coord := lease.NewCoordinator(pool, *sweep)
	coord.Start()
	defer coord.Stop()

	fmt.Printf("=== 租约配额池 demo: 总配额=%d, 协调器巡检周期=%s ===\n\n", pool.Total(), *sweep)

	fmt.Printf("[1] 业务 A 租赁 80 个配额, 承诺借期 %s (优先级 NORMAL)\n", *ttl)
	leaseA, err := pool.Grant("tenant-A", 80, *ttl, lease.PriorityNormal)
	must(err)
	fmt.Printf("    -> 租约 %s 发放, 到期时间 %s, 剩余配额=%d\n\n",
		leaseA.ID, leaseA.ExpiresAt.Format("15:04:05.000"), pool.Available())

	fmt.Println("[2] 业务 A 到期未归还, 等待协调器强制回收 (Revoke)...")
	for {
		snap, _ := pool.Lease(leaseA.ID)
		if snap.Revoked {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Printf("    -> 租约 %s 已被协调器吊销, 剩余配额=%d\n\n", leaseA.ID, pool.Available())

	fmt.Println("[3] 高优业务 B 申请 50 个配额 (优先级 HIGH)")
	leaseB, err := pool.Grant("tenant-B", 50, 30*time.Second, lease.PriorityHigh)
	must(err)
	fmt.Printf("    -> 租约 %s 发放, 剩余配额=%d\n\n", leaseB.ID, pool.Available())

	fmt.Println("[4] 业务 B 按时归还")
	must(pool.Return(leaseB.ID))
	fmt.Printf("    -> 剩余配额=%d\n\n", pool.Available())

	fmt.Printf("=== 审计日志共 %d 条, 其中违约惩罚 (PENALTY) %d 条 ===\n",
		len(pool.Audit()), countPenalty(pool.Audit()))
}

func countPenalty(audit []lease.AuditEvent) int {
	n := 0
	for _, e := range audit {
		if e.Type == lease.EventPenalty {
			n++
		}
	}
	return n
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
