// 运行样例:主节点 A 收到滚动重启指令,冻结本地写入并向候选节点 B
// 移交租约凭证,B 无缝接管主角色,全过程业务写入零错误。
package main

import (
	"fmt"
	"sync/atomic"
	"time"

	"leasectrl/lease"
)

func main() {
	coord := lease.NewCoordinator()
	nodeA := lease.NewNode("A", coord)
	nodeB := lease.NewNode("B", coord)
	defer nodeA.Shutdown()
	defer nodeB.Shutdown()

	// 1. A 获取租约,成为主节点,附带业务上下文。
	ctx := map[string]string{"shard": "7", "offset": "1024"}
	must(nodeA.Acquire("order-service", 500*time.Millisecond, ctx))
	fmt.Println("[启动] A 获取租约 order-service, 成为主节点")

	// 2. 业务客户端持续写入(自动跟随当前主节点)。
	var writes, errs int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := clientWrite(coord, "order-service"); err != nil {
				atomic.AddInt64(&errs, 1)
			} else {
				atomic.AddInt64(&writes, 1)
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	fmt.Printf("[运行] A 动态续期保活中, 已处理写入 %d 次\n", atomic.LoadInt64(&writes))

	// 3. A 收到滚动重启指令:冻结本地写入,向 B 发起主动移交。
	fmt.Println("[重启] A 收到滚动重启指令, 冻结写入并请求向 B 移交")
	start := time.Now()
	d, err := nodeA.GracefulHandover("B")
	must(err)
	fmt.Printf("[移交] B 收到交接包并无缝接管主角色, 切换耗时 %v (墙钟 %v)\n",
		d, time.Since(start))

	// 4. 移交后业务继续,由 B 提供服务。
	time.Sleep(100 * time.Millisecond)
	close(stop)
	primary, _ := coord.Primary("order-service")
	l, _ := nodeB.Lease()
	fmt.Printf("[完成] 当前主节点: %s, fencing token: %d, 继承上下文: %v\n",
		primary, l.FencingToken, l.Context)
	fmt.Printf("[统计] 总写入 %d 次, 错误 %d 次 (零错误中断)\n",
		atomic.LoadInt64(&writes), atomic.LoadInt64(&errs))
}

func clientWrite(c *lease.Coordinator, leaseID string) error {
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		id, err := c.Primary(leaseID)
		if err == nil {
			if n := c.Node(id); n != nil {
				if werr := n.Write("event"); werr == nil {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return lease.ErrHandoverFailed
		}
		time.Sleep(time.Millisecond)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
