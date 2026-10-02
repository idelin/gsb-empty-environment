// 运行样例：模拟系统时间被回拨 1 小时，守护器立即熔断全部租约并降级安全模式。
package main

import (
	"fmt"
	"time"

	"clockguard"
)

func main() {
	// 演示用可操控时钟；生产环境替换为 clockguard.NewRealClock()。
	clock := clockguard.NewFakeClock(time.Now())
	manager := clockguard.NewLeaseManager(clock)
	guard := clockguard.NewGuardian(clock, manager, 5*time.Second, 100*time.Millisecond)

	guard.OnTrip(func(ev clockguard.TripEvent) {
		fmt.Printf("\n[ALARM] %s\n", ev.Reason)
	})

	// 1. 正常发放 3 个租约。
	for _, id := range []string{"order-svc", "pay-svc", "stock-svc"} {
		l, _ := manager.Acquire(id, time.Hour)
		fmt.Printf("[lease] 发放租约 %-10s 过期时间 %s\n", l.ID, l.ExpiresAt.Format(time.RFC3339))
	}

	// 2. 正常运行若干采样周期，双时钟步调一致，一切正常。
	guard.Start()
	for i := 0; i < 3; i++ {
		clock.Advance(100 * time.Millisecond)
		time.Sleep(120 * time.Millisecond)
	}
	fmt.Printf("[ok]   双时钟步调一致，活跃租约 %d 个，安全模式=%v\n",
		len(manager.Active()), manager.SafeMode())

	// 3. 模拟运维人员将系统时间向前回拨 1 小时。
	fmt.Println("\n>>> 模拟：系统时间被回拨 1 小时 ...")
	clock.JumpWall(-time.Hour)

	// 4. 下一拍守护器即检测到撕裂并熔断。
	time.Sleep(300 * time.Millisecond)
	guard.Stop()

	fmt.Printf("[trip] 守护器已熔断: tripped=%v\n", guard.Tripped())
	fmt.Printf("[trip] 活跃租约数: %d（已全部强制注销）\n", len(manager.Active()))
	fmt.Printf("[trip] 安全模式: %v\n", manager.SafeMode())
	fmt.Printf("[trip] 熔断原因: %s\n", manager.TripReason())

	// 5. 安全模式下拒绝新租约。
	if _, err := manager.Acquire("new-svc", time.Minute); err != nil {
		fmt.Printf("[safe] 新租约被拒绝: %v\n", err)
	}
}
