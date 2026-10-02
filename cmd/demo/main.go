// 演示：节点 A 持锁后假死 -> 租约 5 秒超时 -> 节点 B 获锁 ->
// A 恢复后写操作被 FenceToken 拒绝。
package main

import (
	"context"
	"fmt"
	"time"

	"distlock"
)

var start = time.Now()

func logf(format string, args ...any) {
	fmt.Printf("[%6.1fs] %s\n", time.Since(start).Seconds(), fmt.Sprintf(format, args...))
}

func main() {
	svc := distlock.NewLockService()
	nodeA := distlock.NewClient(svc, "node-A")
	nodeB := distlock.NewClient(svc, "node-B")
	ctx := context.Background()
	const ttl = 5 * time.Second

	lockA, err := nodeA.Acquire(ctx, "resource", ttl)
	if err != nil {
		panic(err)
	}
	logf("node-A 获取锁成功 (fence token=%d, 租约=%v)，心跳已启动", lockA.Token(), ttl)
	if err := lockA.Write("written-by-A"); err != nil {
		panic(err)
	}
	logf("node-A 写入资源: %q", svc.Read("resource"))

	lockA.Freeze()
	logf("node-A 假死挂起，心跳停止 ...")

	type acquired struct {
		lock *distlock.Lock
		err  error
	}
	bCh := make(chan acquired, 1)
	go func() {
		l, err := nodeB.Acquire(ctx, "resource", ttl)
		bCh <- acquired{l, err}
	}()
	logf("node-B 排队等待锁 ...")

	r := <-bCh
	if r.err != nil {
		panic(r.err)
	}
	lockB := r.lock
	logf("租约超时，node-B 获取锁成功 (fence token=%d)", lockB.Token())

	lockA.Unfreeze()
	logf("node-A 从假死中恢复，尝试写操作 ...")
	if err := lockA.Write("stale-write-by-A"); err != nil {
		logf("node-A 写被拒绝: %v", err)
	} else {
		logf("!! node-A 的过期写竟然成功了（不应发生）")
	}

	if err := lockB.Write("written-by-B"); err != nil {
		panic(err)
	}
	logf("node-B 写入资源成功，当前资源值: %q", svc.Read("resource"))

	time.Sleep(2 * time.Second) // 等 A 的心跳发现锁丢失
	logf("node-A 心跳检测到锁丢失: %v", lockA.Lost())

	lockB.Unlock()
	logf("node-B 释放锁，演示结束")
}
