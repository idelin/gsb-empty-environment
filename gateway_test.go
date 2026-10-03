package gateway

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// 测试拓扑：华北（北京）、华南（深圳）、海外（美西）三个集群，每集群两个节点。
func testNodes() []*Node {
	return []*Node{
		{ID: "cn-north-1", Cluster: "华北", Region: "华北", City: "北京", Country: "CN",
			Lat: 39.9042, Lng: 116.4074, CIDRs: []string{"111.192.0.0/12"}},
		{ID: "cn-north-2", Cluster: "华北", Region: "华北", City: "北京", Country: "CN",
			Lat: 39.9042, Lng: 116.4074, CIDRs: []string{"111.192.0.0/12"}},
		{ID: "cn-south-1", Cluster: "华南", Region: "华南", City: "深圳", Country: "CN",
			Lat: 22.5431, Lng: 114.0579, CIDRs: []string{"113.108.0.0/16"}},
		{ID: "cn-south-2", Cluster: "华南", Region: "华南", City: "深圳", Country: "CN",
			Lat: 22.5431, Lng: 114.0579, CIDRs: []string{"113.108.0.0/16"}},
		{ID: "overseas-1", Cluster: "海外", Region: "美西", City: "圣何塞", Country: "US",
			Lat: 37.3382, Lng: -121.8863, CIDRs: []string{"8.8.8.0/24"}},
		{ID: "overseas-2", Cluster: "海外", Region: "美西", City: "圣何塞", Country: "US",
			Lat: 37.3382, Lng: -121.8863, CIDRs: []string{"8.8.8.0/24"}},
	}
}

var beijingClient = net.ParseIP("111.196.80.15")

// 验收边界 1：北京客户端秒级识别并优先路由至华北集群。
func TestRouteBeijingClientToNorthCluster(t *testing.T) {
	r := NewRouter(testNodes())
	defer r.Close()

	for i := 0; i < 50; i++ {
		d, err := r.Route(fmt.Sprintf("user-%d", i), beijingClient)
		if err != nil {
			t.Fatalf("路由失败: %v", err)
		}
		if d.Node.Cluster != "华北" {
			t.Fatalf("key=%q 期望华北集群，实际 %s", d.Key, d.Node.Cluster)
		}
		if d.Tier != "同城" {
			t.Fatalf("期望同城层级，实际 %s", d.Tier)
		}
		if d.EstRTT > 5*time.Millisecond {
			t.Fatalf("同城估算 RTT 过大: %s", d.EstRTT)
		}
	}
}

// 验收边界 2：华北全宕机时平滑重选华南，且不流向海外。
func TestFailoverToSouthWhenNorthDown(t *testing.T) {
	nodes := testNodes()
	r := NewRouter(nodes)
	defer r.Close()

	before, err := r.Route("user-42", beijingClient)
	if err != nil || before.Node.Cluster != "华北" {
		t.Fatalf("前置条件失败: %v %v", before, err)
	}

	// 华北集群全部宕机。
	for _, n := range nodes {
		if n.Cluster == "华北" {
			n.SetHealth(false)
		}
	}

	for i := 0; i < 50; i++ {
		d, err := r.Route(fmt.Sprintf("user-%d", i), beijingClient)
		if err != nil {
			t.Fatalf("华北宕机后路由失败: %v", err)
		}
		if d.Node.Cluster != "华南" {
			t.Fatalf("期望平滑重选华南，实际 %s", d.Node.Cluster)
		}
		if d.Tier != "同国就近" {
			t.Fatalf("期望同国就近层级，实际 %s", d.Tier)
		}
		if d.DistanceKM < 1000 {
			t.Fatalf("北京到深圳距离应约 1900km，实际 %.0fkm", d.DistanceKM)
		}
	}
}

// 跨国流量精准拦截降级：海外客户端只匹配海外网段，境内无其同国节点。
func TestCrossBorderInterceptedAndDegraded(t *testing.T) {
	r := NewRouter(testNodes())
	defer r.Close()

	usClient := net.ParseIP("8.8.8.8")
	d, err := r.Route("user-1", usClient)
	if err != ErrCrossBorder {
		t.Fatalf("期望 ErrCrossBorder，实际 %v", err)
	}
	if !d.Degraded || d.Node != nil {
		t.Fatalf("期望降级且无节点，实际 %+v", d)
	}
}

// 境内全部宕机、仅剩海外健康节点时，同样拦截降级而非跨境转发。
func TestDomesticOutageNeverCrossesBorder(t *testing.T) {
	nodes := testNodes()
	r := NewRouter(nodes)
	defer r.Close()
	for _, n := range nodes {
		if n.Country == "CN" {
			n.SetHealth(false)
		}
	}
	d, err := r.Route("user-1", beijingClient)
	if err != ErrCrossBorder || !d.Degraded {
		t.Fatalf("期望跨国拦截降级，实际 %+v err=%v", d, err)
	}
}

// 一致性哈希：同 key 稳定命中同节点；摘除一个节点后大部分 key 不迁移。
func TestConsistentHashStabilityAndSmoothness(t *testing.T) {
	nodes := testNodes()
	r := NewRouter(nodes)
	defer r.Close()

	first := make(map[string]string)
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("shard-%d", i)
		d, _ := r.Route(key, beijingClient)
		first[key] = d.Node.ID
		// 同 key 再次路由必须稳定。
		d2, _ := r.Route(key, beijingClient)
		if d2.Node.ID != d.Node.ID {
			t.Fatalf("key=%q 路由不稳定: %s vs %s", key, d.Node.ID, d2.Node.ID)
		}
	}

	// 宕掉华北一个节点，另一个仍健康：key 应只在华北两节点间迁移。
	for _, n := range nodes {
		if n.ID == "cn-north-1" {
			n.SetHealth(false)
		}
	}
	moved := 0
	for key, id := range first {
		d, _ := r.Route(key, beijingClient)
		if d.Node.Cluster != "华北" {
			t.Fatalf("华北仍有健康节点，不应迁出华北，实际 %s", d.Node.Cluster)
		}
		if d.Node.ID != id {
			moved++
		}
	}
	if moved == 0 || moved >= len(first) {
		t.Fatalf("平滑重选异常：迁移 %d/%d", moved, len(first))
	}
	t.Logf("摘除 1/2 华北节点后迁移 %d/%d 个 key（约一半，符合一致性哈希预期）", moved, len(first))
}

// 秒级发现：后台健康检查在约 1 秒内识别宕机并切换路由。
func TestHealthProbeDetectsFailureWithinASecond(t *testing.T) {
	nodes := testNodes()
	var northUp int32 = 1
	prober := func(n *Node) (time.Duration, bool) {
		if n.Cluster == "华北" {
			return 2 * time.Millisecond, atomic.LoadInt32(&northUp) == 1
		}
		return 30 * time.Millisecond, true
	}
	r := NewRouter(nodes, WithProber(prober, 200*time.Millisecond))
	defer r.Close()

	d, err := r.Route("user-7", beijingClient)
	if err != nil || d.Node.Cluster != "华北" {
		t.Fatalf("初始应路由华北: %+v err=%v", d, err)
	}

	atomic.StoreInt32(&northUp, 0) // 模拟华北集群整体故障

	deadline := time.Now().Add(2 * time.Second)
	for {
		d, err = r.Route("user-7", beijingClient)
		if err == nil && d.Node.Cluster == "华南" {
			return // 秒级内完成重选
		}
		if time.Now().After(deadline) {
			t.Fatalf("2 秒内未完成故障转移，当前: %+v err=%v", d, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// 基础函数：经纬度距离与 RTT 估算。
func TestHaversineAndRTT(t *testing.T) {
	d := HaversineKM(39.9042, 116.4074, 22.5431, 114.0579) // 北京-深圳
	if d < 1800 || d > 2100 {
		t.Fatalf("北京-深圳距离异常: %.0fkm", d)
	}
	n := &Node{}
	if rtt := EstimateRTT(n, 0); rtt != time.Millisecond {
		t.Fatalf("同城保底 RTT 应为 1ms，实际 %s", rtt)
	}
	n.MeasuredRTT = 7 * time.Millisecond
	if rtt := EstimateRTT(n, 5000); rtt != 7*time.Millisecond {
		t.Fatalf("实测 RTT 应优先，实际 %s", rtt)
	}
}

// 最长前缀匹配：更精确的网段优先定位。
func TestLocateLongestPrefixMatch(t *testing.T) {
	nodes := testNodes()
	nodes = append(nodes, &Node{
		ID: "cn-north-vip", Cluster: "华北", Region: "华北", City: "北京", Country: "CN",
		Lat: 39.9042, Lng: 116.4074, CIDRs: []string{"111.196.80.0/24"},
	})
	r := NewRouter(nodes)
	defer r.Close()
	loc, ok := r.Locate(net.ParseIP("111.196.80.99"))
	if !ok || loc.City != "北京" {
		t.Fatalf("定位失败: %+v ok=%v", loc, ok)
	}
	if _, ok := r.Locate(net.ParseIP("203.0.113.1")); ok {
		t.Fatal("未知网段不应定位成功")
	}
}
