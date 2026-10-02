// demo 模拟一个 3 节点集群，演示 Lease Read 优化器的两种工作模式。
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"leaseread"
)

func main() {
	ctx := context.Background()

	// 构建 3 节点集群，模拟 2ms 网络延迟。
	nodes := map[int]*leaseread.Node{}
	for i := 0; i < 3; i++ {
		nodes[i] = &leaseread.Node{ID: i, Store: leaseread.NewKVStore()}
	}
	transport := leaseread.NewSimTransport(nodes, 2*time.Millisecond)
	lm := leaseread.NewLeaseManager(10 * time.Millisecond) // 时钟漂移安全余量
	ro := leaseread.NewReadOptimizer(0, nodes[0], []int{1, 2}, transport, lm)

	fmt.Println("=== 阶段 0：主节点通过多数派心跳获取 5s 租约 ===")
	if err := lm.Renew(ctx, transport, 0, []int{1, 2}, 5*time.Second); err != nil {
		panic(err)
	}
	fmt.Printf("租约已授予，剩余安全窗口 ≈ %v（心跳 RPC: %d 次）\n\n",
		lm.Remaining().Round(time.Millisecond), transport.HeartbeatCallsCount())

	if err := ro.Write(ctx, "hot-key", "latest-value", 1); err != nil {
		panic(err)
	}

	fmt.Println("=== 阶段 1：租约安全窗口内，10,000 次并发读 ===")
	baseCalls := transport.TotalCalls()
	const reads = 10000
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < reads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := ro.Read(ctx, "hot-key"); err != nil {
				panic(err)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("完成 %d 次读，耗时 %v，吞吐 %.0f reads/s\n", reads, elapsed.Round(time.Millisecond), float64(reads)/elapsed.Seconds())
	fmt.Printf("本地读: %d，降级读: %d，读路径网络调用: %d（零网络开销 ✓）\n\n",
		ro.LocalReads.Load(), ro.QuorumReads.Load(), transport.TotalCalls()-baseCalls)

	fmt.Println("=== 阶段 2：租约剩余不足 100ms，自动降级 ===")
	lm.Grant(50 * time.Millisecond) // 模拟租约即将耗尽
	fmt.Printf("剩余安全窗口 ≈ %v < 100ms 阈值\n", lm.Remaining().Round(time.Millisecond))
	baseFetch := transport.FetchCallsCount()
	v, err := ro.Read(ctx, "hot-key")
	if err != nil {
		panic(err)
	}
	fmt.Printf("读结果: %+v（走多数派网络确认，Fetch RPC: %d 次）\n\n",
		v, transport.FetchCallsCount()-baseFetch)

	fmt.Println("=== 阶段 3：续租后恢复零网络快路径 ===")
	if err := lm.Renew(ctx, transport, 0, []int{1, 2}, 5*time.Second); err != nil {
		panic(err)
	}
	baseCalls = transport.TotalCalls()
	if _, err := ro.Read(ctx, "hot-key"); err != nil {
		panic(err)
	}
	fmt.Printf("续租成功，读路径网络调用: %d（恢复本地读 ✓）\n", transport.TotalCalls()-baseCalls)
}
