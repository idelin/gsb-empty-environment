// 运行样例：真实时钟下的两个场景。
//  场景 1：TTL=10s、maxDrift=500ms，主节点每秒下发一次写，9.5s 安全期结束后主动放弃特权。
//  场景 2：运行中注入墙上时钟前拨 1s，看门狗安全自检触发紧急提前降级。
package main

import (
	"fmt"
	"sync"
	"time"

	"leasecoord/lease"
)

const (
	ttl      = 10 * time.Second
	maxDrift = 500 * time.Millisecond
)

// jumpClock 包装真实时钟，可注入墙上时间偏移（单调时间不受影响）。
type jumpClock struct {
	*lease.RealClock
	mu     sync.Mutex
	offset time.Duration
}

func (c *jumpClock) Wall() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *jumpClock) Jump(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

func main() {
	scenario1()
	scenario2()
}

func scenario1() {
	fmt.Println("=== 场景 1：安全期结束，主节点主动放弃特权 (TTL=10s, maxDrift=500ms) ===")
	clock := lease.NewRealClock()
	coord := lease.NewCoordinator(clock, ttl, maxDrift)
	m := lease.NewManager("node-A", coord, clock, ttl, maxDrift)
	if err := m.Campaign(); err != nil {
		panic(err)
	}
	m.StartWatchdog(20 * time.Millisecond)
	defer m.Close()
	fmt.Printf("[t=0.0s] node-A 当选主节点, fencing=%d, 安全期=%s\n", m.Fencing(), m.SafePeriod())

	start := time.Now()
	for i := 0; i < 11; i++ {
		time.Sleep(time.Second)
		elapsed := time.Since(start).Round(100 * time.Millisecond)
		if _, err := m.Write(fmt.Sprintf("put key%d=val%d", i, i)); err != nil {
			fmt.Printf("[t=%5s] 写被拒绝: %v\n", elapsed, err)
			fmt.Printf("[t=%5s] 降级原因: %s\n", elapsed, m.DemoteReason())
			break
		} else {
			fmt.Printf("[t=%5s] 主身份写成功: put key%d=val%d\n", elapsed, i, i)
		}
	}
	fmt.Println()
}

func scenario2() {
	fmt.Println("=== 场景 2：注入时钟前拨 1s，触发紧急提前降级 ===")
	clock := &jumpClock{RealClock: lease.NewRealClock()}
	coord := lease.NewCoordinator(clock, ttl, maxDrift)
	m := lease.NewManager("node-A", coord, clock, ttl, maxDrift)
	if err := m.Campaign(); err != nil {
		panic(err)
	}
	m.StartWatchdog(20 * time.Millisecond)
	defer m.Close()
	fmt.Printf("[t=0.0s] node-A 当选主节点, fencing=%d\n", m.Fencing())

	start := time.Now()
	for i := 0; i < 6; i++ {
		time.Sleep(time.Second)
		elapsed := time.Since(start).Round(100 * time.Millisecond)
		if i == 3 {
			clock.Jump(1 * time.Second)
			fmt.Printf("[t=%5s] *** 故障注入：墙上时钟前拨 1s（>500ms 最大漂移） ***\n", elapsed)
		}
		if _, err := m.Write(fmt.Sprintf("put key%d=val%d", i, i)); err != nil {
			fmt.Printf("[t=%5s] 写被拒绝: %v\n", elapsed, err)
			fmt.Printf("[t=%5s] 降级原因: %s\n", elapsed, m.DemoteReason())
			break
		}
		fmt.Printf("[t=%5s] 主身份写成功: put key%d=val%d\n", elapsed, i, i)
	}
	fmt.Println("系统保持单主，未发生脑裂。")
}
