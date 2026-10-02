// demo 演示：单点 Seed + 50 个存量成员，3 个全新节点冷启动入网。
package main

import (
	"context"
	"fmt"
	"time"

	"seedboot/discovery"
)

func main() {
	// 单机演示使用进程内内存网络；真实多机部署删除此行即走默认 TCP。
	defer discovery.UseInMemoryTransport()()

	seed := discovery.NewSeed()
	if err := seed.Start(); err != nil {
		panic(err)
	}
	defer seed.Close()
	for i := 0; i < 50; i++ {
		seed.Register(discovery.NodeMeta{
			ID:        fmt.Sprintf("member-%02d", i),
			Addr:      fmt.Sprintf("127.0.0.1:%d", 20000+i),
			Heartbeat: time.Now().UnixNano(),
		})
	}
	fmt.Printf("seed listening at %s, holding %d members\n", seed.Addr, 50)

	nodes := make([]*discovery.Node, 3)
	start := time.Now()
	for i := range nodes {
		nodes[i] = discovery.NewNode(fmt.Sprintf("fresh-%d", i), []string{seed.Addr},
			discovery.WithGossipInterval(50*time.Millisecond), discovery.WithFanout(5))
		if err := nodes[i].Bootstrap(context.Background()); err != nil {
			panic(err)
		}
	}
	fmt.Printf("3 fresh nodes bootstrapped in %v\n", time.Since(start))
	for _, n := range nodes {
		fmt.Printf("  %s @ %s knows %d members\n", n.ID(), n.Addr(), n.MemberCount())
	}
	defer func() {
		for _, n := range nodes {
			n.Close()
		}
	}()

	time.Sleep(time.Second) // 让 Gossip 洗牌环跑若干轮直至收敛
	for _, n := range nodes {
		fmt.Printf("after gossip: %s knows %d members\n", n.ID(), n.MemberCount())
	}
	fmt.Printf("seed now holds %d members\n", len(seed.Members()))
}
