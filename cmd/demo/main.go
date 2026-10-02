// 行级悲观锁 + 死锁预防器 演示程序
// 场景: Wound-Wait 策略下
//  1) 老事务T1持有行X锁, 新事务T2请求 -> T2挂起等待, T1提交后T2获锁
//  2) 新事务T3持有行X锁, 老事务T1请求 -> T1直接抢占(Wound)T3, T3被迫重启
package main

import (
	"fmt"
	"time"

	"lockdemo/lockmgr"
)

func main() {
	m := lockmgr.NewManager(lockmgr.WoundWait)
	fmt.Printf("=== 行级悲观锁演示 (策略: %s, txnID越小事务越老) ===\n\n", lockmgr.WoundWait)

	// 场景1: 老事务持有X锁, 新事务请求 -> 新事务挂起等待
	fmt.Println("--- 场景1: T1(老)持有rowA的X锁, T2(新)请求X锁 ---")
	mustLock(m, 1, "rowA", lockmgr.Exclusive)
	fmt.Println("[T1] 获得 rowA X锁")

	done := make(chan error, 1)
	go func() { done <- m.Lock(2, "rowA", lockmgr.Exclusive, 3*time.Second) }()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-done:
		fmt.Printf("[T2] 意外返回: %v\n", err)
	default:
		fmt.Println("[T2] 请求rowA X锁 -> 被挂起, 等待T1释放...")
	}
	time.Sleep(300 * time.Millisecond)
	m.Commit(1)
	fmt.Println("[T1] 提交, 释放rowA")
	if err := <-done; err != nil {
		fmt.Printf("[T2] 加锁失败: %v\n", err)
	} else {
		fmt.Println("[T2] 获得 rowA X锁")
	}
	m.Commit(2)

	// 场景2: 新事务持有X锁, 老事务请求 -> 老事务抢占(Wound), 新事务被迫重启
	fmt.Println("\n--- 场景2: T3(新)持有rowB的X锁, T1(老)请求X锁 ---")
	mustLock(m, 3, "rowB", lockmgr.Exclusive)
	fmt.Println("[T3] 获得 rowB X锁")

	if err := m.Lock(1, "rowB", lockmgr.Exclusive, time.Second); err != nil {
		fmt.Printf("[T1] 加锁失败: %v\n", err)
	} else {
		fmt.Println("[T1] 请求rowB X锁 -> 立即抢占(Wound)T3, 获得锁")
	}
	fmt.Printf("[T3] 已被中止(被迫重启): %v\n", m.IsAborted(3))
	if err := m.Lock(3, "rowC", lockmgr.Shared, 0); err == lockmgr.ErrAborted {
		fmt.Println("[T3] 中止状态下任何锁请求均返回 ErrAborted")
	}

	// 新事务回滚并重启
	m.Restart(3)
	fmt.Println("[T3] 回滚并重启")
	m.Commit(1)
	mustLock(m, 3, "rowB", lockmgr.Exclusive)
	fmt.Println("[T3] 重启后重新获得 rowB X锁")
	m.Commit(3)

	// 场景3: 对比 Wait-Die 策略
	fmt.Println("\n--- 场景3: Wait-Die 策略对比 ---")
	m2 := lockmgr.NewManager(lockmgr.WaitDie)
	mustLock(m2, 1, "rowA", lockmgr.Exclusive)
	fmt.Println("[T1] 获得 rowA X锁")
	if err := m2.Lock(2, "rowA", lockmgr.Exclusive, time.Second); err == lockmgr.ErrAborted {
		fmt.Println("[T2] 请求老事务持有的锁 -> 自杀(Die), 等待重启")
	}
	m2.Commit(1)
	m2.Restart(2)
	mustLock(m2, 2, "rowA", lockmgr.Exclusive)
	fmt.Println("[T2] T1提交后重启, 获得 rowA X锁")

	fmt.Println("\n=== 演示结束: 系统全程无死锁 ===")
}

func mustLock(m *lockmgr.Manager, txnID uint64, row string, mode lockmgr.LockMode) {
	if err := m.Lock(txnID, row, mode, 3*time.Second); err != nil {
		panic(fmt.Sprintf("T%d 请求 %s %s锁失败: %v", txnID, row, mode, err))
	}
}
