// 演示三个关键性质：
//  1. 两任务互换阶段（A: CPU->IO，B: IO->CPU）仍然有进展，不死锁；
//  2. 容量为 0 的阶段无法满足，靠取消退出；
//  3. 任务异常（返回错误）后，所有额度回归初值。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"quota"
)

func phaseName(p quota.Phase) string {
	switch p {
	case quota.CPU:
		return "CPU"
	case quota.IO:
		return "IO"
	default:
		return "?"
	}
}

func snapshot(m *quota.Manager) string {
	return fmt.Sprintf("CPU used=%d, IO used=%d", m.Usage(quota.CPU), m.Usage(quota.IO))
}

func main() {
	// 场景 1：互换阶段不死锁（CPU=1, IO=1）。
	fmt.Println("== 场景1：两任务互换阶段（容量各 1） ==")
	m := quota.NewManager(map[quota.Phase]int{quota.CPU: 1, quota.IO: 1})
	ctx := context.Background()

	go func() {
		err := m.Run(ctx, []quota.Phase{quota.CPU, quota.IO, quota.CPU}, func(p quota.Phase) error {
			fmt.Printf("任务A 进入 %-3s（%s）\n", phaseName(p), snapshot(m))
			time.Sleep(100 * time.Millisecond)
			return nil
		})
		fmt.Printf("任务A 结束 err=%v\n", err)
	}()
	go func() {
		err := m.Run(ctx, []quota.Phase{quota.IO, quota.CPU, quota.IO}, func(p quota.Phase) error {
			time.Sleep(20 * time.Millisecond) // 错开一点启动，便于观察
			fmt.Printf("任务B 进入 %-3s（%s）\n", phaseName(p), snapshot(m))
			time.Sleep(100 * time.Millisecond)
			return nil
		})
		fmt.Printf("任务B 结束 err=%v\n", err)
	}()
	time.Sleep(700 * time.Millisecond)
	fmt.Printf("互换场景完成后：%s\n\n", snapshot(m))

	// 场景 2：IO 容量为 0，等待只能靠取消退出。
	fmt.Println("== 场景2：IO 容量为 0，取消等待 ==")
	m2 := quota.NewManager(map[quota.Phase]int{quota.CPU: 1, quota.IO: 0})
	cctx, cancel := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		done2 <- m2.Run(cctx, []quota.Phase{quota.CPU, quota.IO}, func(p quota.Phase) error {
			fmt.Printf("任务  进入 %s（%s），随后准备切换\n", phaseName(p), snapshot(m2))
			return nil
		})
	}()
	time.Sleep(150 * time.Millisecond)
	fmt.Printf("正在等待容量0的 IO（%s），现在取消……\n", snapshot(m2))
	cancel()
	fmt.Printf("任务退出 err=%v；取消后：%s\n\n", <-done2, snapshot(m2))

	// 场景 3：任务函数返回错误（异常结束），额度全部回归。
	fmt.Println("== 场景3：任务异常返回错误，额度回归初值 ==")
	m3 := quota.NewManager(map[quota.Phase]int{quota.CPU: 2, quota.IO: 1})
	errBoom := errors.New("注入的 IO 故障")
	err := m3.Run(context.Background(), []quota.Phase{quota.CPU, quota.IO}, func(p quota.Phase) error {
		fmt.Printf("任务  进入 %s（%s）\n", phaseName(p), snapshot(m3))
		if p == quota.IO {
			return errBoom
		}
		return nil
	})
	fmt.Printf("Run 返回 err=%v\n", err)
	fmt.Printf("异常结束后：%s（应全部为 0，等于初值）\n", snapshot(m3))
}
