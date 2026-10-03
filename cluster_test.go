package memberlist

import (
	"sync"
	"testing"
)

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b Version
		want int
	}{
		{Version{1, 1}, Version{1, 1}, 0},
		{Version{2, 0}, Version{1, 9}, 1},
		{Version{1, 2}, Version{1, 1}, 1},
		{Version{1, 1}, Version{1, 2}, -1},
		{Version{1, 0}, Version{2, 0}, -1},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("Compare(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestMergeVersionArbitration(t *testing.T) {
	c := NewCluster("n1", "mem://1", Meta{Zone: "us-east", Role: "worker", Weight: 10}, nil)

	// 构造一个版本更旧的远端视图，应被仲裁拒绝。
	self := c.Self()
	stale := self
	stale.Meta.Weight = 999
	stale.Version = Version{Epoch: self.Version.Epoch, Seq: self.Version.Seq - 1}
	if c.Merge([]Node{stale}) {
		t.Fatal("stale version should not change local state")
	}
	if got := c.Self().Meta.Weight; got != 10 {
		t.Fatalf("stale merge overwrote weight: got %d, want 10", got)
	}

	// 同版本重复投递应幂等（无变更）。
	echo := c.Self()
	if c.Merge([]Node{echo}) {
		t.Fatal("duplicate same-version merge should be a no-op")
	}

	// 版本更高的远端视图应赢得仲裁（模拟本节点更新经 gossip 回传）。
	c.UpdateMeta(Meta{Zone: "us-east", Role: "worker", Weight: 80})
	newer := c.Self()
	c.UpdateMeta(Meta{Zone: "us-east", Role: "worker", Weight: 1})
	newer.Version.Seq = c.Self().Version.Seq + 1
	if !c.Merge([]Node{newer}) {
		t.Fatal("newer version should win arbitration")
	}
	if got := c.Self().Meta.Weight; got != 80 {
		t.Fatalf("weight = %d, want 80", got)
	}
}

func TestConcurrentMetaUpdates(t *testing.T) {
	c := NewCluster("n1", "mem://1", Meta{Zone: "us-east", Weight: 10}, nil)
	const goroutines = 64
	const updatesPer = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < updatesPer; i++ {
				c.UpdateMeta(Meta{Zone: "us-east", Weight: base + i})
			}
		}(g * updatesPer)
	}
	wg.Wait()

	self := c.Self()
	// 初始版本 Seq=1，每次更新 Seq+1，总计 1 + 64*50。
	wantSeq := uint64(1 + goroutines*updatesPer)
	if self.Version.Seq != wantSeq {
		t.Fatalf("version seq = %d, want %d (lost update under concurrency)", self.Version.Seq, wantSeq)
	}
}

func TestMergeDiscoversNewNode(t *testing.T) {
	c1 := NewCluster("n1", "mem://1", Meta{Zone: "us-east", Weight: 10}, nil)
	c2 := NewCluster("n2", "mem://2", Meta{Zone: "us-west", Weight: 20}, map[string]string{"disk": "ssd"})

	if !c1.Merge(c2.Snapshot()) {
		t.Fatal("merge should report change for new node")
	}
	n, ok := c1.Get("n2")
	if !ok {
		t.Fatal("n2 not discovered")
	}
	if n.Meta.Zone != "us-west" || n.Labels["disk"] != "ssd" {
		t.Fatalf("unexpected merged node: %+v", n)
	}
	// 深拷贝校验：修改远端 labels 不应影响本地。
	c2.SetLabel("disk", "hdd")
	n, _ = c1.Get("n2")
	if n.Labels["disk"] != "ssd" {
		t.Fatal("labels map shared between clusters, want deep copy")
	}
}
