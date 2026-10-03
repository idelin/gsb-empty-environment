package memberlist

import "testing"

func newRouterCluster() *Cluster {
	c := NewCluster("self", "mem://0", Meta{Zone: "us-east", Role: "gateway", Weight: 5}, nil)
	c.Merge([]Node{
		{ID: "a", Addr: "mem://a", Meta: Meta{Zone: "us-east", Role: "worker", Weight: 10}, Version: Version{1, 1}, Heartbeat: 1},
		{ID: "b", Addr: "mem://b", Meta: Meta{Zone: "us-east", Role: "worker", Weight: 80}, Version: Version{1, 1}, Heartbeat: 1},
		{ID: "c", Addr: "mem://c", Meta: Meta{Zone: "us-west", Role: "worker", Weight: 100}, Version: Version{1, 1}, Heartbeat: 1},
	})
	return c
}

func TestRouterQueryHighestWeight(t *testing.T) {
	r := NewRouter(newRouterCluster())
	n, err := r.Query("zone=us-east")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "b" || n.Meta.Weight != 80 {
		t.Fatalf("Query returned %+v, want node b (weight 80)", n)
	}
}

func TestRouterQueryAllOrdering(t *testing.T) {
	r := NewRouter(newRouterCluster())
	nodes, err := r.QueryAll("role=worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("got %d nodes, want 3", len(nodes))
	}
	if nodes[0].ID != "c" || nodes[1].ID != "b" || nodes[2].ID != "a" {
		t.Fatalf("wrong weight ordering: %v", []string{nodes[0].ID, nodes[1].ID, nodes[2].ID})
	}
}

func TestRouterExcludesDeadNodes(t *testing.T) {
	c := newRouterCluster()
	r := NewRouter(c)

	// 直接构造一个 Dead 的 b 节点（更高版本，模拟状态传播）。
	dead := Node{ID: "b", Addr: "mem://b", Meta: Meta{Zone: "us-east", Role: "worker", Weight: 80},
		Version: Version{1, 2}, Heartbeat: 1, Status: StatusDead}
	c.mu.Lock()
	b := c.nodes["b"]
	b.Status = StatusDead
	b.Version = dead.Version
	c.mu.Unlock()

	n, err := r.Query("zone=us-east")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "a" {
		t.Fatalf("dead node not excluded: got %s, want a", n.ID)
	}

	// 全部不满足时返回 ErrNoHealthyNode。
	if _, err := r.Query("zone=eu-central"); err != ErrNoHealthyNode {
		t.Fatalf("got %v, want ErrNoHealthyNode", err)
	}
}
