package memberlist

import (
	"fmt"
	"testing"
	"time"
)

type testNode struct {
	cluster  *Cluster
	gossiper *Gossiper
	tr       *MemTransport
}

func newTestNode(t *testing.T, net *MemNetwork, id, addr string, meta Meta, labels map[string]string) *testNode {
	t.Helper()
	tr, err := net.NewMemTransport(addr)
	if err != nil {
		t.Fatal(err)
	}
	c := NewCluster(id, addr, meta, labels)
	g := NewGossiper(c, tr, 20*time.Millisecond, 300*time.Millisecond)
	return &testNode{cluster: c, gossiper: g, tr: tr}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

// 验收场景：动态更新某节点负载权重 10 -> 80，经 Gossip 同步后，
// 路由查询 "zone=us-east" 返回最新健康且权重最高的节点。
func TestAcceptanceWeightUpdateGossipAndRoute(t *testing.T) {
	net := NewMemNetwork()

	n1 := newTestNode(t, net, "n1", "mem://n1",
		Meta{Zone: "us-east", Role: "worker", Weight: 50}, map[string]string{"disk": "ssd"})
	n2 := newTestNode(t, net, "n2", "mem://n2",
		Meta{Zone: "us-east", Role: "worker", Weight: 10}, map[string]string{"disk": "nvme"})
	n3 := newTestNode(t, net, "n3", "mem://n3",
		Meta{Zone: "us-west", Role: "gateway", Weight: 90}, nil)

	n1.gossiper.Start()
	defer n1.gossiper.Stop()
	// n2、n3 通过 n1 引导加入。
	n2.gossiper.Start()
	defer n2.gossiper.Stop()
	n3.gossiper.Start()
	defer n3.gossiper.Stop()
	if err := n2.gossiper.Join("mem://n1"); err != nil {
		t.Fatal(err)
	}
	if err := n3.gossiper.Join("mem://n1"); err != nil {
		t.Fatal(err)
	}

	// 1. 等待成员列表收敛：每个节点都应看到 3 个成员。
	for i, tn := range []*testNode{n1, n2, n3} {
		tn := tn
		waitFor(t, 2*time.Second, fmt.Sprintf("node %d discovers all members", i+1), func() bool {
			return len(tn.cluster.Snapshot()) == 3
		})
	}

	// 2. 初始路由：us-east 中 n1(50) 权重最高。
	r1 := NewRouter(n1.cluster)
	best, err := r1.Query("zone=us-east")
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "n1" {
		t.Fatalf("initial best = %s, want n1 (weight 50)", best.ID)
	}

	// 3. 动态更新 n2 的负载权重 10 -> 80。
	n2.cluster.UpdateMeta(Meta{Zone: "us-east", Role: "worker", Weight: 80})

	// 4. 等待 Gossip 把新元数据同步到 n1 与 n3。
	for _, tn := range []*testNode{n1, n3} {
		tn := tn
		waitFor(t, 2*time.Second, "weight=80 propagated to "+tn.cluster.SelfID(), func() bool {
			n, ok := tn.cluster.Get("n2")
			return ok && n.Meta.Weight == 80
		})
	}

	// 5. 路由查询应返回最新健康且权重最高的 n2。
	best, err = r1.Query("zone=us-east")
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "n2" || best.Meta.Weight != 80 {
		t.Fatalf("after update best = %+v, want n2 with weight 80", best)
	}

	// 6. 组合条件：zone=us-east 且 weight>=60 且标签 disk=nvme。
	best, err = r1.Query("zone=us-east,weight>=60,label.disk=nvme")
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "n2" {
		t.Fatalf("compound selector best = %s, want n2", best.ID)
	}
}

func TestGossipExcludesFailedNode(t *testing.T) {
	net := NewMemNetwork()
	n1 := newTestNode(t, net, "n1", "mem://n1", Meta{Zone: "us-east", Weight: 10}, nil)
	n2 := newTestNode(t, net, "n2", "mem://n2", Meta{Zone: "us-east", Weight: 80}, nil)

	n1.gossiper.Start()
	defer n1.gossiper.Stop()
	n2.gossiper.Start()
	if err := n2.gossiper.Join("mem://n1"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 2*time.Second, "mutual discovery", func() bool {
		return len(n1.cluster.Snapshot()) == 2 && len(n2.cluster.Snapshot()) == 2
	})

	// n2 崩溃：停止 gossip 并断开传输。
	n2.gossiper.Stop()
	n2.tr.Close()

	// 超过 deadTimeout 后，n1 应将 n2 标记为 Dead 并在路由中排除。
	r1 := NewRouter(n1.cluster)
	waitFor(t, 2*time.Second, "n2 marked dead and excluded from routing", func() bool {
		best, err := r1.Query("zone=us-east")
		return err == nil && best.ID == "n1"
	})
}
