// Package gateway 实现自适应局部感知分片网关：
// 维护节点地理经纬度与 IP 网段，根据客户端来源 IP 计算物理距离 / 估算 RTT，
// 将请求通过一致性哈希路由至同城 / 同局域网（同区域）节点，
// 并对跨国流量进行精准拦截降级。
package gateway

import (
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrCrossBorder 表示请求只能路由到海外节点，被网关拦截降级。
var ErrCrossBorder = errors.New("gateway: cross-border traffic intercepted, degraded")

// ErrNoHealthyNode 表示境内无任何健康节点可用。
var ErrNoHealthyNode = errors.New("gateway: no healthy domestic node")

// 光纤中信号往返速度经验值：约 100km/ms（含设备开销）。
const kmPerMillisecond = 100.0

// Node 表示一个网关节点。
type Node struct {
	ID       string   // 节点唯一标识
	Cluster  string   // 集群名，如 "华北" / "华南" / "海外"
	Region   string   // 大区，用于"同局域网/同区域"判定
	City     string   // 城市，用于"同城"判定
	Country  string   // 国家代码，如 "CN" / "US"
	Lat, Lng float64  // 地理经纬度
	CIDRs    []string // 节点负责的客户端 IP 网段（地理定位库）

	// MeasuredRTT 为可选的实测 RTT；非零时优先于地理距离估算。
	MeasuredRTT time.Duration

	up int32 // 原子健康标记：1 健康，0 宕机
}

// Up 报告节点是否健康。
func (n *Node) Up() bool { return atomic.LoadInt32(&n.up) == 1 }

// SetHealth 设置节点健康状态（由健康检查器或运维调用）。
func (n *Node) SetHealth(up bool) {
	if up {
		atomic.StoreInt32(&n.up, 1)
	} else {
		atomic.StoreInt32(&n.up, 0)
	}
}

// Location 是客户端来源 IP 解析出的地理位置。
type Location struct {
	IP      net.IP
	Country string
	Region  string
	City    string
	Lat     float64
	Lng     float64
}

// Decision 是一次路由决策的结果。
type Decision struct {
	Node       *Node         // 命中的节点（降级时为 nil）
	Key        string        // 请求分片键
	Tier       string        // 命中层级：同城 / 同区域 / 同国就近 / 降级
	DistanceKM float64       // 客户端到节点的物理距离
	EstRTT     time.Duration // 估算或实测 RTT
	Degraded   bool          // 是否被拦截降级
	Reason     string        // 决策说明
}

func (d Decision) String() string {
	if d.Degraded {
		return fmt.Sprintf("[降级] key=%q 原因=%s", d.Key, d.Reason)
	}
	return fmt.Sprintf("key=%q -> 节点=%s 集群=%s 层级=%s 距离=%.0fkm 估算RTT=%s",
		d.Key, d.Node.ID, d.Node.Cluster, d.Tier, d.DistanceKM, d.EstRTT)
}

// Prober 探测节点健康，返回实测 RTT。ok=false 表示节点宕机。
type Prober func(n *Node) (rtt time.Duration, ok bool)

type prefixEntry struct {
	network *net.IPNet
	node    *Node
}

// Router 是局部感知分片路由器。
type Router struct {
	mu          sync.RWMutex
	nodes       []*Node
	prefixes    []prefixEntry
	vnodes      int    // 每个节点的虚拟节点数
	homeCountry string // 服务所在国；跨国流量拦截降级

	prober     Prober
	probeEvery time.Duration
	stopCh     chan struct{}
	probeDone  chan struct{}
}

// Option 用于定制 Router。
type Option func(*Router)

// WithVirtualNodes 设置一致性哈希环上每个节点的虚拟节点数。
func WithVirtualNodes(n int) Option {
	return func(r *Router) { r.vnodes = n }
}

// WithHomeCountry 设置服务所在国（默认 "CN"），其余国家来源一律拦截降级。
func WithHomeCountry(cc string) Option {
	return func(r *Router) { r.homeCountry = cc }
}

// WithProber 设置健康检查器与探测周期（秒级发现宕机）。
func WithProber(p Prober, every time.Duration) Option {
	return func(r *Router) {
		r.prober = p
		r.probeEvery = every
	}
}

// NewRouter 构建路由器并启动后台健康检查（如配置了 Prober）。
func NewRouter(nodes []*Node, opts ...Option) *Router {
	r := &Router{
		vnodes:      100,
		homeCountry: "CN",
		probeEvery:  time.Second,
		stopCh:      make(chan struct{}),
		probeDone:   make(chan struct{}),
	}
	for _, o := range opts {
		o(r)
	}
	for _, n := range nodes {
		n.SetHealth(true)
		r.nodes = append(r.nodes, n)
		for _, c := range n.CIDRs {
			_, ipnet, err := net.ParseCIDR(c)
			if err == nil {
				r.prefixes = append(r.prefixes, prefixEntry{network: ipnet, node: n})
			}
		}
	}
	// 最长前缀优先，保证定位精准。
	sort.Slice(r.prefixes, func(i, j int) bool {
		mi, _ := r.prefixes[i].network.Mask.Size()
		mj, _ := r.prefixes[j].network.Mask.Size()
		return mi > mj
	})
	if r.prober != nil {
		go r.probeLoop()
	} else {
		close(r.probeDone)
	}
	return r
}

// Close 停止后台健康检查。
func (r *Router) Close() {
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
	<-r.probeDone
}

func (r *Router) probeLoop() {
	defer close(r.probeDone)
	ticker := time.NewTicker(r.probeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.probeOnce()
		}
	}
}

func (r *Router) probeOnce() {
	r.mu.RLock()
	nodes := make([]*Node, len(r.nodes))
	copy(nodes, r.nodes)
	r.mu.RUnlock()
	for _, n := range nodes {
		rtt, ok := r.prober(n)
		n.SetHealth(ok)
		if ok && rtt > 0 {
			n.MeasuredRTT = rtt
		}
	}
}

// Locate 根据客户端来源 IP 解析地理位置（最长前缀匹配节点网段库）。
func (r *Router) Locate(ip net.IP) (Location, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.prefixes {
		if e.network.Contains(ip) {
			n := e.node
			return Location{
				IP:      ip,
				Country: n.Country,
				Region:  n.Region,
				City:    n.City,
				Lat:     n.Lat,
				Lng:     n.Lng,
			}, true
		}
	}
	return Location{IP: ip}, false
}

// Route 为携带分片键 key、来源为 clientIP 的请求选择节点。
//
// 选择层级（均为健康节点）：
//  1. 同城节点
//  2. 同区域（同局域网）节点
//  3. 同国节点中物理距离 / RTT 最近者
//  4. 仅剩海外节点 -> 跨国拦截，返回降级决策与 ErrCrossBorder
func (r *Router) Route(key string, clientIP net.IP) (Decision, error) {
	loc, known := r.Locate(clientIP)
	if !known {
		// 未知来源：按未知地理位置处理，仅允许同国未知为假，走降级以保护数据合规。
		return Decision{Key: key, Degraded: true, Reason: "来源IP无法定位，按跨国流量拦截降级"}, ErrCrossBorder
	}
	if loc.Country != r.homeCountry {
		return Decision{Key: key, Degraded: true, Reason: fmt.Sprintf("客户端位于 %s，跨国流量精准拦截降级", loc.Country)}, ErrCrossBorder
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var sameCity, sameRegion, sameCountry []*Node
	var overseas bool
	for _, n := range r.nodes {
		if n.Country != loc.Country {
			if n.Up() {
				overseas = true
			}
			continue
		}
		if !n.Up() {
			continue
		}
		switch {
		case n.City == loc.City && n.City != "":
			sameCity = append(sameCity, n)
		case n.Region == loc.Region && n.Region != "":
			sameRegion = append(sameRegion, n)
		default:
			sameCountry = append(sameCountry, n)
		}
	}

	pick := func(candidates []*Node, tier string) Decision {
		n := r.hashPick(key, candidates)
		dist := HaversineKM(loc.Lat, loc.Lng, n.Lat, n.Lng)
		return Decision{
			Node:       n,
			Key:        key,
			Tier:       tier,
			DistanceKM: dist,
			EstRTT:     EstimateRTT(n, dist),
			Reason:     fmt.Sprintf("客户端定位 %s/%s/%s，命中%s", loc.Country, loc.Region, loc.City, tier),
		}
	}

	switch {
	case len(sameCity) > 0:
		return pick(sameCity, "同城"), nil
	case len(sameRegion) > 0:
		return pick(sameRegion, "同区域"), nil
	case len(sameCountry) > 0:
		// 同国内按距离选最近集群，再在集群内哈希，保证平滑。
		best := nearestNodes(sameCountry, loc)
		return pick(best, "同国就近"), nil
	case overseas:
		return Decision{Key: key, Degraded: true, Reason: "境内无健康节点，跨国流量精准拦截降级"}, ErrCrossBorder
	default:
		return Decision{Key: key, Degraded: true, Reason: "无任何健康节点"}, ErrNoHealthyNode
	}
}

// hashPick 在候选节点上构建一致性哈希环，为 key 选择节点。
// 候选集合稳定时选择结果稳定；节点增删只影响 1/N 的 key，实现平滑重选。
func (r *Router) hashPick(key string, candidates []*Node) *Node {
	type point struct {
		hash uint32
		node *Node
	}
	ring := make([]point, 0, len(candidates)*r.vnodes)
	for _, n := range candidates {
		for i := 0; i < r.vnodes; i++ {
			h := crc32.ChecksumIEEE([]byte(n.ID + "#" + strconv.Itoa(i)))
			ring = append(ring, point{hash: h, node: n})
		}
	}
	sort.Slice(ring, func(i, j int) bool { return ring[i].hash < ring[j].hash })
	h := crc32.ChecksumIEEE([]byte(key))
	idx := sort.Search(len(ring), func(i int) bool { return ring[i].hash >= h })
	if idx == len(ring) {
		idx = 0
	}
	return ring[idx].node
}

// nearestNodes 返回距离客户端最近的同集群节点集合。
func nearestNodes(nodes []*Node, loc Location) []*Node {
	bestDist := math.MaxFloat64
	bestCluster := ""
	for _, n := range nodes {
		d := HaversineKM(loc.Lat, loc.Lng, n.Lat, n.Lng)
		if d < bestDist {
			bestDist = d
			bestCluster = n.Cluster
		}
	}
	var out []*Node
	for _, n := range nodes {
		if n.Cluster == bestCluster {
			out = append(out, n)
		}
	}
	return out
}

// HaversineKM 计算两经纬度间的球面距离（公里）。
func HaversineKM(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadiusKM = 6371.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKM * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// EstimateRTT 估算客户端到节点的 RTT：优先实测值，否则按物理距离推算。
func EstimateRTT(n *Node, distanceKM float64) time.Duration {
	if n.MeasuredRTT > 0 {
		return n.MeasuredRTT
	}
	ms := distanceKM / kmPerMillisecond
	if ms < 1 {
		ms = 1 // 同城局域网保底 1ms
	}
	return time.Duration(ms * float64(time.Millisecond))
}
