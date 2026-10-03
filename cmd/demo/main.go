// demo 在单进程内启动 3 个 gossip 集群节点，
// 演示：动态权重更新 10 -> 80 经 gossip 收敛后，
// 路由查询 "zone=us-east" 返回最新健康且权重最高的节点。
//
// 默认使用进程内内存传输；加 -udp 可切换为 UDP 传输（127.0.0.1:700x）。
package main

import (
	"flag"
	"fmt"
	"time"

	"memberlist"
)

var useUDP = flag.Bool("udp", false, "use UDP transport instead of in-memory transport")

var memNet = memberlist.NewMemNetwork()

type demoNode struct {
	cluster  *memberlist.Cluster
	gossiper *memberlist.Gossiper
}

func mustStart(id, addr string, meta memberlist.Meta, labels map[string]string) *demoNode {
	var tr memberlist.Transport
	var err error
	if *useUDP {
		tr, err = memberlist.NewUDPTransport(addr)
	} else {
		tr, err = memNet.NewMemTransport(addr)
	}
	if err != nil {
		panic(err)
	}
	c := memberlist.NewCluster(id, tr.Addr(), meta, labels)
	g := memberlist.NewGossiper(c, tr, 100*time.Millisecond, 2*time.Second)
	g.Start()
	return &demoNode{cluster: c, gossiper: g}
}

func waitFor(what string, cond func() bool) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			fmt.Printf("[ok] %s\n", what)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	panic("timeout: " + what)
}

func main() {
	flag.Parse()
	addrOf := func(port string) string {
		if *useUDP {
			return "127.0.0.1:" + port
		}
		return "mem://node" + port
	}

	n1 := mustStart("n1", addrOf("7001"),
		memberlist.Meta{Zone: "us-east", Role: "worker", Weight: 50},
		map[string]string{"disk": "ssd"})
	n2 := mustStart("n2", addrOf("7002"),
		memberlist.Meta{Zone: "us-east", Role: "worker", Weight: 10},
		map[string]string{"disk": "nvme"})
	n3 := mustStart("n3", addrOf("7003"),
		memberlist.Meta{Zone: "us-west", Role: "gateway", Weight: 90}, nil)
	defer n1.gossiper.Stop()
	defer n2.gossiper.Stop()
	defer n3.gossiper.Stop()

	// n2、n3 以 n1 为种子加入集群。
	seed := n1.cluster.Self().Addr
	if err := n2.gossiper.Join(seed); err != nil {
		panic(err)
	}
	if err := n3.gossiper.Join(seed); err != nil {
		panic(err)
	}

	waitFor("成员列表收敛（每节点可见 3 个成员）", func() bool {
		return len(n1.cluster.Snapshot()) == 3 &&
			len(n2.cluster.Snapshot()) == 3 &&
			len(n3.cluster.Snapshot()) == 3
	})

	router := NewRouterAt(n1.cluster)
	best, err := router.Query("zone=us-east")
	if err != nil {
		panic(err)
	}
	fmt.Printf("[route] 初始查询 zone=us-east -> %s (weight=%d)\n", best.ID, best.Meta.Weight)

	fmt.Println("[update] n2 负载权重 10 -> 80")
	n2.cluster.UpdateMeta(memberlist.Meta{Zone: "us-east", Role: "worker", Weight: 80})

	waitFor("gossip 同步：n1、n3 观测到 n2.weight=80", func() bool {
		a, ok1 := n1.cluster.Get("n2")
		b, ok2 := n3.cluster.Get("n2")
		return ok1 && ok2 && a.Meta.Weight == 80 && b.Meta.Weight == 80
	})

	best, err = router.Query("zone=us-east")
	if err != nil {
		panic(err)
	}
	fmt.Printf("[route] 更新后查询 zone=us-east -> %s (weight=%d, zone=%s, role=%s)\n",
		best.ID, best.Meta.Weight, best.Meta.Zone, best.Meta.Role)

	best, err = router.Query("zone=us-east,weight>=60,label.disk=nvme")
	if err != nil {
		panic(err)
	}
	fmt.Printf("[route] 组合筛选 zone=us-east,weight>=60,label.disk=nvme -> %s\n", best.ID)
}

// NewRouterAt 是 demo 内的小包装，仅为输出可读性。
func NewRouterAt(c *memberlist.Cluster) *memberlist.Router {
	return memberlist.NewRouter(c)
}
