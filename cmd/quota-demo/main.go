// quota-demo 演示两阶段额度的切换与取消。
// 运行：go run ./cmd/quota-demo
package main

import (
	"context"
	"fmt"
	"time"

	"phasequota"
)

func main() {
	sched := quota.New(map[quota.Phase]int{quota.CPU: 1, quota.IO: 1})

	// 任务 A：CPU -> IO。
	taskA := sched.NewTask()
	ctx := context.Background()
	if err := taskA.Acquire(ctx, quota.CPU); err != nil {
		panic(err)
	}
	fmt.Printf("A holds CPU; available cpu=%d io=%d\n",
		sched.Available(quota.CPU), sched.Available(quota.IO))

	// 切换到 IO：先释放 CPU，再等待 IO（此时 IO 空闲，立即获得）。
	if err := taskA.Switch(ctx, quota.IO); err != nil {
		panic(err)
	}
	fmt.Printf("A holds IO;  available cpu=%d io=%d\n",
		sched.Available(quota.CPU), sched.Available(quota.IO))
	taskA.Release()

	// 容量为 0 的阶段无法获取，使用带超时的上下文取消退出。
	zero := quota.New(map[quota.Phase]int{quota.CPU: 1, quota.IO: 0})
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	taskB := zero.NewTask()
	if err := taskB.Acquire(cctx, quota.IO); err != nil {
		fmt.Printf("zero-capacity IO canceled: %v\n", err)
	}
	fmt.Printf("zero scheduler restored: cpu=%d io=%d\n",
		zero.Available(quota.CPU), zero.Available(quota.IO))
}
