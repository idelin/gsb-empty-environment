package shardgw

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

var (
	locBeijing   = Location{Country: "CN", Region: "华北", City: "北京", Lat: 39.9042, Lng: 116.4074}
	locShenzhen  = Location{Country: "CN", Region: "华南", City: "深圳", Lat: 22.5431, Lng: 114.0579}
	locSingapore = Location{Country: "SG", Region: "海外", City: "新加坡", Lat: 1.3521, Lng: 103.8198}
	locNewYork   = Location{Country: "US", Region: "海外", City: "纽约", Lat: 40.7128, Lng: -74.0060}
)

// newTestGateway 搭建 华北 / 华南 / 海外 三集群拓扑，
// 并注册北京、深圳、美国的客户端网段。
func newTestGateway(t *testing.T) (*Gateway, map[string][]*Node) {
	t.Helper()
	geo := NewGeoDB()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(geo.Add("111.192.0.0/12", locBeijing)) // 北京联通段
	must(geo.Add("14.16.0.0/12", locShenzhen))  // 广东电信段
	must(geo.Add("8.8.8.0/24", locNewYork))     // 境外段
	must(geo.Add("10.8.0.0/16", locBeijing))    // 北京机房内网段

	north := NewCluster("华北", locBeijing)
	south := NewCluster("华南", locShenzhen)
	oversea := NewCluster("海外", locSingapore)

	nodes := map[string][]*Node{}
	add := func(c *Cluster, ids ...string) {
		for _, id := range ids {
			n := &Node{ID: id, Addr: id + ":8080", Loc: c.Loc}
			n.SetHealthy(true)
			c.AddNode(n)
			nodes[c.Name] = append(nodes[c.Name], n)
		}
	}
	add(north, "north-1", "north-2", "north-3")
	add(south, "south-1", "south-2", "south-3")
	add(oversea, "oversea-1", "oversea-2")

	return NewGateway(geo, "CN", north, south, oversea), nodes
}

func setClusterHealth(nodes map[string][]*Node, cluster string, healthy bool) {
	for _, n := range nodes[cluster] {
		n.SetHealthy(healthy)
	}
}

// 验收 1：北京客户端秒级识别并优先路由到华北集群。
func TestBeijingClientPrefersNorthChina(t *testing.T) {
	gw, _ := newTestGateway(t)
	for _, ip := range []string{"111.195.1.10", "111.200.33.7", "111.192.0.1"} {
		d, err := gw.Route(ip)
		if err != nil {
			t.Fatalf("route %s: %v", ip, err)
		}
		if d.Cluster != "华北" {
			t.Fatalf("client %s routed to %q, want 华北", ip, d.Cluster)
		}
		if d.ClientLoc.City != "北京" {
			t.Fatalf("client %s resolved to city %q, want 北京", ip, d.ClientLoc.City)
		}
		if d.Elapsed > time.Second {
			t.Fatalf("routing decision took %s, want sub-second", d.Elapsed)
		}
	}
}

// 验收 2：华北全宕机时平滑重选华南（而不是海外或直接失败）。
func TestFailoverToSouthChinaWhenNorthDown(t *testing.T) {
	gw, nodes := newTestGateway(t)
	setClusterHealth(nodes, "华北", false)
	defer setClusterHealth(nodes, "华北", true)

	d, err := gw.Route("111.195.1.10")
	if err != nil {
		t.Fatalf("route after 华北 outage: %v", err)
	}
	if d.Cluster != "华南" {
		t.Fatalf("failover routed to %q, want 华南", d.Cluster)
	}
	if d.Degraded {
		t.Fatal("domestic failover must not be degraded")
	}
}

// 华北部分宕机时仍留在华北（同城优先），且只选健康节点。
func TestPartialOutageStaysInNorthChina(t *testing.T) {
	gw, nodes := newTestGateway(t)
	nodes["华北"][0].SetHealthy(false)
	nodes["华北"][1].SetHealthy(false)
	defer setClusterHealth(nodes, "华北", true)

	for i := 0; i < 50; i++ {
		d, err := gw.Route(fmt.Sprintf("111.195.1.%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if d.Cluster != "华北" {
			t.Fatalf("partial outage routed to %q, want 华北", d.Cluster)
		}
		if !d.Node.Healthy() {
			t.Fatalf("routed to unhealthy node %s", d.Node.ID)
		}
	}
}

// 验收 3：跨国流量精准拦截降级。
func TestCrossBorderTrafficDegraded(t *testing.T) {
	gw, _ := newTestGateway(t)
	d, err := gw.Route("8.8.8.8")
	if !errors.Is(err, ErrCrossBorder) {
		t.Fatalf("err = %v, want ErrCrossBorder", err)
	}
	if !d.Degraded {
		t.Fatal("overseas client must be degraded")
	}
}

// 国内流量永不落到海外集群（即便国内全部宕机）。
func TestDomesticNeverRoutedOverseas(t *testing.T) {
	gw, nodes := newTestGateway(t)
	setClusterHealth(nodes, "华北", false)
	setClusterHealth(nodes, "华南", false)
	defer setClusterHealth(nodes, "华北", true)
	defer setClusterHealth(nodes, "华南", true)

	_, err := gw.Route("111.195.1.10")
	if !errors.Is(err, ErrNoHealthyNode) {
		t.Fatalf("err = %v, want ErrNoHealthyNode (overseas cluster must be excluded)", err)
	}
}

// 同局域网节点优先：客户端 IP 命中节点声明的网段时直达该节点。
func TestSameLANNodePreferred(t *testing.T) {
	gw, nodes := newTestGateway(t)
	subnets, err := ParseSubnets("10.8.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	nodes["华南"][0].Subnets = subnets // 故意把内网段挂在华南节点上

	d, err := gw.Route("10.8.1.25")
	if err != nil {
		t.Fatal(err)
	}
	if d.Node != nodes["华南"][0] {
		t.Fatalf("same-LAN client routed to %s, want %s", d.Node.ID, nodes["华南"][0].ID)
	}
	if d.Reason != "same-LAN hit" {
		t.Fatalf("reason = %q, want same-LAN hit", d.Reason)
	}
}

// 一致性哈希：同一客户端稳定映射同一节点；单节点宕机只影响其承载的 key。
func TestConsistentHashStability(t *testing.T) {
	gw, nodes := newTestGateway(t)
	const clients = 200
	before := make(map[string]string, clients)
	for i := 0; i < clients; i++ {
		ip := fmt.Sprintf("111.195.%d.%d", i/256, i%256)
		d, err := gw.Route(ip)
		if err != nil {
			t.Fatal(err)
		}
		before[ip] = d.Node.ID
		// 重复路由必须稳定
		d2, _ := gw.Route(ip)
		if d2.Node.ID != d.Node.ID {
			t.Fatalf("unstable mapping for %s: %s -> %s", ip, d.Node.ID, d2.Node.ID)
		}
	}

	// 宕掉一个华北节点，统计重新映射比例。
	victim := nodes["华北"][0]
	victim.SetHealthy(false)
	defer victim.SetHealthy(true)

	remapped := 0
	for ip, id := range before {
		d, err := gw.Route(ip)
		if err != nil {
			t.Fatal(err)
		}
		if d.Cluster != "华北" {
			t.Fatalf("single node outage caused cluster switch for %s", ip)
		}
		if d.Node.ID != id {
			remapped++
			if id != victim.ID {
				t.Fatalf("key of healthy node %s remapped to %s", id, d.Node.ID)
			}
		}
	}
	t.Logf("remapped %d/%d keys after losing %s", remapped, clients, victim.ID)
	if remapped > clients/2 {
		t.Fatalf("too many keys remapped: %d/%d", remapped, clients)
	}
}

// 深圳客户端就近进入华南集群。
func TestShenzhenClientPrefersSouthChina(t *testing.T) {
	gw, _ := newTestGateway(t)
	d, err := gw.Route("14.17.22.1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Cluster != "华南" {
		t.Fatalf("routed to %q, want 华南", d.Cluster)
	}
}

// 路由决策为微秒级，远优于“秒级识别”要求。
func TestRouteDecisionLatency(t *testing.T) {
	gw, _ := newTestGateway(t)
	start := time.Now()
	const n = 10000
	for i := 0; i < n; i++ {
		if _, err := gw.Route(fmt.Sprintf("111.195.%d.%d", i/256, i%256)); err != nil {
			t.Fatal(err)
		}
	}
	avg := time.Since(start) / n
	t.Logf("avg routing decision: %s", avg)
	if avg > 10*time.Millisecond {
		t.Fatalf("avg decision latency %s too slow", avg)
	}
}

// 健康探测：探针把华北标记为宕机后，网关自动重选华南。
func TestProberDrivesFailover(t *testing.T) {
	gw, nodes := newTestGateway(t)
	down := map[string]bool{}
	stop := gw.StartProber(20*time.Millisecond, func(n *Node) (time.Duration, error) {
		if down[n.ID] {
			return 0, errors.New("unreachable")
		}
		return 5 * time.Millisecond, nil
	})
	defer stop()

	d, err := gw.Route("111.195.1.10")
	if err != nil || d.Cluster != "华北" {
		t.Fatalf("before outage: cluster=%s err=%v", d.Cluster, err)
	}
	for _, n := range nodes["华北"] {
		down[n.ID] = true
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d, err = gw.Route("111.195.1.10")
		if err == nil && d.Cluster == "华南" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("prober did not drive failover, last: cluster=%s err=%v", d.Cluster, err)
}

func TestHaversineKm(t *testing.T) {
	d := HaversineKm(locBeijing, locShenzhen)
	if d < 1800 || d > 2100 {
		t.Fatalf("Beijing-Shenzhen distance %.0fkm out of range", d)
	}
	if HaversineKm(locBeijing, locBeijing) != 0 {
		t.Fatal("zero distance expected")
	}
}

func TestGeoDBLongestPrefix(t *testing.T) {
	db := NewGeoDB()
	if err := db.Add("111.192.0.0/12", locBeijing); err != nil {
		t.Fatal(err)
	}
	specific := locBeijing
	specific.City = "北京-海淀"
	if err := db.Add("111.196.0.0/16", specific); err != nil {
		t.Fatal(err)
	}
	loc, ok := db.Lookup(net.ParseIP("111.196.3.4"))
	if !ok || loc.City != "北京-海淀" {
		t.Fatalf("longest prefix failed: %+v ok=%v", loc, ok)
	}
	loc, ok = db.Lookup(net.ParseIP("111.200.3.4"))
	if !ok || loc.City != "北京" {
		t.Fatalf("fallback prefix failed: %+v ok=%v", loc, ok)
	}
	if _, ok := db.Lookup(net.ParseIP("1.2.3.4")); ok {
		t.Fatal("unexpected hit for unknown IP")
	}
}
