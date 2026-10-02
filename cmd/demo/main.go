// demo 完整演示验收场景:
//
//	机房1 主 / 机房2 备 -> 光纤挖断、机房1 整体掉电 -> 法官确认失联并向机房2
//	颁发晋升租约 -> 机房1 恢复后检测到凭证失效,自动切换为只读备机。
//
// 运行: go run ./cmd/demo
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"leasefed/controller"
	"leasefed/store"
	"leasefed/witness"
)

// link 模拟机房到法官的物理链路。
type link struct {
	inner controller.Client
	cut   atomic.Bool
}

func (l *link) Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error) {
	if l.cut.Load() {
		return witness.Lease{}, false, errors.New("光纤挖断: 链路不可达")
	}
	return l.inner.Acquire(holder, ttl)
}

func (l *link) Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error) {
	if l.cut.Load() {
		return witness.Lease{}, false, errors.New("光纤挖断: 链路不可达")
	}
	return l.inner.Renew(holder, epoch, ttl)
}

func writeThrough(name string, c *controller.Controller, s *store.Store, key, val string) {
	tok, ok := c.FencingToken()
	if !ok {
		log.Printf("[%s] 写入 %q 被拒绝: 当前是只读备机,无有效租约", name, key)
		return
	}
	if err := s.Put(tok, key, val); err != nil {
		log.Printf("[%s] 写入 %q 被存储层拒绝: %v", name, key, err)
		return
	}
	log.Printf("[%s] 写入 %q=%q 成功 (fencing token epoch=%d)", name, key, val, tok)
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	const ttl = 600 * time.Millisecond

	// 第三方机房: 轻量级法官
	w := witness.NewCore()
	log.Printf("[witness] 法官已就位(第三方机房), 租约 TTL=%v", ttl)

	// 两个机房的控制器
	dc1Link := &link{inner: &controller.LocalClient{Core: w}}
	dc2Link := &link{inner: &controller.LocalClient{Core: w}}
	dc1 := controller.New("dc1", dc1Link, ttl)
	dc2 := controller.New("dc2", dc2Link, ttl)
	storage := store.New()

	watch := func(name string) func(controller.State, controller.State) {
		return func(from, to controller.State) {
			log.Printf("[%s] 角色变更: %v -> %v", name, from, to)
		}
	}
	dc1.OnStateChange(watch("dc1"))
	dc2.OnStateChange(watch("dc2"))

	ctx1, stop1 := context.WithCancel(context.Background())
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	go dc1.Run(ctx1)
	go dc2.Run(ctx2)

	log.Printf("=== 阶段 1: 正常运行, 机房1 主 / 机房2 备 ===")
	time.Sleep(800 * time.Millisecond)
	writeThrough("dc1", dc1, storage, "order-1", "created-by-dc1")
	writeThrough("dc2", dc2, storage, "order-x", "should-be-rejected")

	log.Printf("=== 阶段 2: 光纤挖断! 机房1 整体掉电, 与法官失联 ===")
	dc1Link.cut.Store(true)
	stop1() // 机房1 掉电: 控制器进程随机器停止
	deadEpoch := dc1.Lease().Epoch
	log.Printf("[dc1] 掉电前持有租约 epoch=%d", deadEpoch)
	time.Sleep(1200 * time.Millisecond) // 法官侧租约过期 = 确认机房1 真正失联

	log.Printf("=== 阶段 3: 法官向机房2 颁发晋升租约 ===")
	time.Sleep(500 * time.Millisecond) // 等待 dc2 下一轮竞选成功
	writeThrough("dc2", dc2, storage, "order-2", "created-by-dc2")

	log.Printf("--- 模拟机房1 掉电前的迟到写入到达存储层 ---")
	if err := storage.Put(deadEpoch, "order-3", "stale-write-from-dc1"); err != nil {
		log.Printf("[storage] 拒绝机房1 的迟到写入: %v", err)
	}

	log.Printf("=== 阶段 4: 机房1 恢复供电, 用旧凭证尝试续租 ===")
	dc1Link.cut.Store(false)
	ctx1b, stop1b := context.WithCancel(context.Background())
	defer stop1b()
	go dc1.Run(ctx1b) // 机房1 重启, 内存中仍以为自己是主, 首次续租即被法官拒绝
	time.Sleep(800 * time.Millisecond)
	writeThrough("dc1", dc1, storage, "order-4", "must-not-happen")

	log.Printf("=== 终态 ===")
	log.Printf("[dc1] 最终角色: %v (降级次数=%d)", dc1.State(), dc1.StepDownCount())
	log.Printf("[dc2] 最终角色: %v", dc2.State())
	for _, k := range []string{"order-1", "order-2", "order-3", "order-4", "order-x"} {
		if v, ok := storage.Get(k); ok {
			fmt.Printf("  存储层 %s = %q\n", k, v)
		} else {
			fmt.Printf("  存储层 %s 不存在(写入已被正确拒绝)\n", k)
		}
	}
	fmt.Printf("  存储层最大 fencing token: epoch=%d\n", storage.LastEpoch())
}
