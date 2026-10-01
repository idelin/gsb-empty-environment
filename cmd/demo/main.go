// demo 模拟验收场景：系统时间被突然回拨 1 小时，
// 守护器在下一瞬间检测到双时钟撕裂，熔断全部租约并进入安全模式。
package main

import (
	"context"
	"fmt"
	"time"

	"clockguard"
)

func main() {
	// 用可编程时钟模拟故障；生产环境替换为 clockguard.NewSystemClock()。
	clock := clockguard.NewFakeClock(time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC))

	guardian := clockguard.NewGuardian(clock,
		clockguard.WithThreshold(5*time.Second),
		clockguard.WithInterval(100*time.Millisecond),
		clockguard.OnRevoke(func(l clockguard.Lease) {
			fmt.Printf("  [revoke] 强制注销租约 %s (holder=%s)\n", l.ID, l.Holder)
		}),
		clockguard.OnTrip(func(ev clockguard.TripEvent) {
			fmt.Printf("\n>>> 熔断！原因: %s\n", ev.Reason)
			fmt.Printf(">>> 墙钟推进 %v，单调时钟推进 %v，撕裂 %v，强制注销 %d 份租约\n\n",
				ev.WallDelta, ev.MonoDelta, ev.Skew, ev.RevokedLeases)
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go guardian.Run(ctx)

	fmt.Println("[1] 系统正常运行，发放 3 份租约...")
	for _, holder := range []string{"worker-a", "worker-b", "worker-c"} {
		l, err := guardian.Acquire(holder)
		if err != nil {
			panic(err)
		}
		fmt.Printf("  [acquire] %s -> %s\n", l.ID, l.Holder)
	}
	time.Sleep(350 * time.Millisecond) // 监视循环正常跑几个周期
	fmt.Printf("[2] 监视中... 活跃租约 %d 份，守护器状态正常\n", guardian.LeaseCount())

	fmt.Println("[3] 故障注入：系统时间被回拨 1 小时！")
	clock.AdvanceWall(-time.Hour)
	time.Sleep(300 * time.Millisecond) // 等待下一瞬间的采样

	fmt.Printf("[4] 熔断后状态：tripped=%v，活跃租约=%d\n", guardian.Tripped(), guardian.LeaseCount())
	if _, err := guardian.Acquire("worker-d"); err != nil {
		fmt.Printf("[5] 安全模式生效，新租约被拒绝: %v\n", err)
	}
}
