// Demo of the acceptance scenario with a real 5-second lease:
//
//  1. Node A acquires the lock (TTL=5s) and starts heartbeats.
//  2. Node B queues for the same lock.
//  3. Node A freezes (heartbeat stops) at t=1s.
//  4. The lease expires naturally at ~t=6s and B acquires the lock.
//  5. A recovers and tries to write with its old fencing token: rejected.
//  6. B writes with the newer token: accepted.
package main

import (
	"context"
	"fmt"
	"time"

	"distlock"
)

var start = time.Now()

func logf(format string, args ...interface{}) {
	elapsed := time.Since(start).Round(100 * time.Millisecond)
	fmt.Printf("[%6s] %s\n", elapsed, fmt.Sprintf(format, args...))
}

func main() {
	srv := distlock.NewServer(distlock.WithSweepInterval(50 * time.Millisecond))
	defer srv.Close()
	store := distlock.NewStorage()
	const ttl = 5 * time.Second

	// Node A acquires the lock; the heartbeat goroutine starts here.
	lockA, err := srv.Lock(context.Background(), "orders", ttl)
	if err != nil {
		logf("A: 获取锁失败: %v", err)
		return
	}
	tokenA := lockA.FenceToken()
	logf("A: 获取锁成功 fence=%d ttl=5s, 心跳已启动", tokenA)

	// Node B queues for the same lock.
	bGot := make(chan *distlock.Lock, 1)
	go func() {
		l, err := srv.Lock(context.Background(), "orders", ttl)
		if err != nil {
			logf("B: 获取锁失败: %v", err)
			return
		}
		bGot <- l
	}()
	time.Sleep(100 * time.Millisecond)
	logf("B: 已在队列中等待锁...")

	// Node A freezes at t=1s: heartbeat stops, lease keeps ticking down.
	time.Sleep(900 * time.Millisecond)
	lockA.SimulateCrash()
	logf("A: 模拟假死挂起, 心跳停止 (租约将在 5s TTL 到期后自然释放)")

	// Wait for B to be granted the lock after the lease expires.
	lockB := <-bGot
	tokenB := lockB.FenceToken()
	logf("B: 租约过期, 成功获取锁 fence=%d (> A 的 fence=%d)", tokenB, tokenA)

	// B writes with the current token: accepted.
	if err := store.Write(tokenB, "order-1", "written-by-B"); err != nil {
		logf("B: 写入失败: %v", err)
	} else {
		v, _ := store.Read("order-1")
		logf("B: 写入成功, 存储内容 order-1=%q", v)
	}

	// A recovers and tries to write with its stale token: fenced out,
	// because the storage has already seen B's higher token.
	if err := store.Write(tokenA, "order-1", "written-by-A"); err != nil {
		logf("A: 恢复后尝试写入, 被拒绝: %v (fence=%d 已过期)", err, tokenA)
	} else {
		logf("A: 写入成功 (不应发生!)")
	}
	if v, _ := store.Read("order-1"); v != "written-by-B" {
		logf("存储被 A 的过期写入污染! 当前值=%q", v)
	} else {
		logf("存储未被污染, 最终值=%q", v)
	}

	lockB.Unlock()
	logf("B: 释放锁, 演示结束")
}
