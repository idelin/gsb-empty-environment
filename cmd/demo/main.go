// demo 演示行级锁管理器在 Wound-Wait 策略下的验收场景。
package main

import (
	"fmt"
	"time"

	"lockdemo/lockmgr"
)

func main() {
	fmt.Println("=== 场景 1: Wound-Wait —— 新事务请求老事务持有的 X 锁 → 挂起等待 ===")
	lm := lockmgr.NewLockManager(lockmgr.WoundWait, 3*time.Second)

	t1 := lm.Begin() // 老事务（时间戳小）
	t2 := lm.Begin() // 新事务（时间戳大）
	fmt.Printf("T1(id=%d, 老)  T2(id=%d, 新)\n", t1.ID, t2.ID)

	must(t1.Lock("row-1", lockmgr.Exclusive))
	fmt.Println("T1 持有 row-1 的 X 锁")

	done := make(chan error, 1)
	go func() { done <- t2.Lock("row-1", lockmgr.Exclusive) }()

	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		fmt.Println("意外：T2 的 Lock 已返回:", err)
	default:
		fmt.Println("T2 请求 row-1 的 X 锁 → 被挂起等待中...")
	}

	fmt.Println("T1 提交，释放锁")
	t1.Commit()
	fmt.Println("T2 被唤醒，获得锁，err =", <-done)
	t2.Commit()

	fmt.Println()
	fmt.Println("=== 场景 2: Wound-Wait —— 老事务请求新事务持有的 X 锁 → 抢占(Wound) ===")
	lm2 := lockmgr.NewLockManager(lockmgr.WoundWait, 3*time.Second)
	old := lm2.Begin()   // 老事务
	young := lm2.Begin() // 新事务
	fmt.Printf("T_old(id=%d)  T_young(id=%d)\n", old.ID, young.ID)

	must(young.Lock("row-2", lockmgr.Exclusive))
	fmt.Println("T_young 持有 row-2 的 X 锁")

	must(old.Lock("row-2", lockmgr.Exclusive))
	fmt.Println("T_old 请求 row-2 的 X 锁 → 直接抢占(Wound) T_young，立即获得锁")
	fmt.Println("T_young 已被中止:", young.Aborted())

	fmt.Println("T_young 以新事务身份重启，等待 T_old 释放锁...")
	restarted := lm2.Begin()
	restartedDone := make(chan error, 1)
	go func() { restartedDone <- restarted.Lock("row-2", lockmgr.Exclusive) }()
	time.Sleep(200 * time.Millisecond)
	old.Commit()
	fmt.Println("T_old 提交后，重启的 T_young 获得锁，err =", <-restartedDone)
	restarted.Commit()

	fmt.Println()
	fmt.Println("=== 场景 3: Wait-Die —— 新事务遇到老事务持锁 → 自杀 ===")
	lm3 := lockmgr.NewLockManager(lockmgr.WaitDie, 3*time.Second)
	w1 := lm3.Begin() // 老
	w2 := lm3.Begin() // 新
	must(w1.Lock("row-3", lockmgr.Exclusive))
	fmt.Println("T1(老) 持有 row-3 的 X 锁")
	err := w2.Lock("row-3", lockmgr.Exclusive)
	fmt.Println("T2(新) 请求 row-3 的 X 锁 → 立即自杀, err =", err)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
