// Package shardgw 实现自适应局部感知分片网关：
// 根据客户端来源 IP 解析地理位置，按物理距离 / RTT 选择最近的健康集群，
// 在集群内用一致性哈希把请求路由到同城 / 同局域网节点，
// 并对跨国流量做精准拦截降级。
package shardgw

import (
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrNoHealthyNode 表示所有候选集群均无健康节点。
	ErrNoHealthyNode = errors.New("shardgw: no healthy node available")
	// ErrCrossBorder 表示请求来源为境外，已被拦截并降级。
	ErrCrossBorder = errors.New("shardgw: cross-border traffic intercepted, degraded")
	// ErrUnknownLocation 表示客户端 IP 无法解析出地理位置。
	ErrUnknownLocation = errors.New("shardgw: client location unknown")
)

// Node 是一个后端分片节点。
type Node struct {
	ID      string
	Cluster string
	Addr    string
	Loc     Location
	Subnets []*net.IPNet // 节点直接服务的局域网网段

	healthy atomic.Bool
	rttNs   atomic.Int64 // 最近一次探测的 RTT，0 表示未知
}

func (n *Node) Healthy() bool          { return n.healthy.Load() }
func (n *Node) SetHealthy(v bool)      { n.healthy.Store(v) }
func (n *Node) RTT() time.Duration     { return time.Duration(n.rttNs.Load()) }
func (n *Node) SetRTT(d time.Duration) { n.rttNs.Store(int64(d)) }

// ServesLAN 判断节点是否直接服务该 IP 所在的局域网。
func (n *Node) ServesLAN(ip net.IP) bool {
	for _, sn := range n.Subnets {
		if sn.Contains(ip) {
			return true
		}
	}
	return false
}

// Cluster 是同地域的一组节点，挂在一个一致性哈希环上。
type Cluster struct {
	Name string
	Loc  Location

	mu    sync.RWMutex
	nodes []*Node
	ring  *hashRing
}

func NewCluster(name string, loc Location) *Cluster {
	return &Cluster{Name: name, Loc: loc, ring: newHashRing(128)}
}

func (c *Cluster) AddNode(n *Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n.Cluster = c.Name
	c.nodes = append(c.nodes, n)
	c.ring.rebuild(c.nodes)
}

func (c *Cluster) Nodes() []*Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Node, len(c.nodes))
	copy(out, c.nodes)
	return out
}

func (c *Cluster) healthyNodes() []*Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []*Node
	for _, n := range c.nodes {
		if n.Healthy() {
			out = append(out, n)
		}
	}
	return out
}

// Decision 是一次路由决策的完整结果。
type Decision struct {
	ClientIP     net.IP
	ClientLoc    Location
	Cluster      string
	Node         *Node
	DistanceKm   float64
	EstimatedRTT time.Duration
	Degraded     bool   // 是否被降级（跨国拦截）
	Reason       string // 决策说明
	Elapsed      time.Duration
}

func (d *Decision) String() string {
	if d.Degraded {
		return fmt.Sprintf("client=%s loc=%s/%s -> DEGRADED (%s) [%s]",
			d.ClientIP, d.ClientLoc.Country, d.ClientLoc.City, d.Reason, d.Elapsed)
	}
	return fmt.Sprintf("client=%s loc=%s/%s -> cluster=%s node=%s dist=%.0fkm rtt~%s [%s]",
		d.ClientIP, d.ClientLoc.Country, d.ClientLoc.City,
		d.Cluster, d.Node.ID, d.DistanceKm, d.EstimatedRTT, d.Elapsed)
}

// Gateway 是分片网关本体。
type Gateway struct {
	geo         *GeoDB
	homeCountry string // 本网关服务的本国代码，境外来源一律降级
	clusters    []*Cluster
}

func NewGateway(geo *GeoDB, homeCountry string, clusters ...*Cluster) *Gateway {
	return &Gateway{geo: geo, homeCountry: homeCountry, clusters: clusters}
}

// Route 为客户端 IP 做一次就近路由决策。
// 决策只读内存态（GeoDB + 健康位 + 哈希环），复杂度与集群数线性相关，
// 单次耗时为微秒级，远低于“秒级识别”的要求。
func (g *Gateway) Route(clientIP string) (*Decision, error) {
	start := time.Now()
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return nil, fmt.Errorf("shardgw: invalid client IP %q", clientIP)
	}
	loc, ok := g.geo.Lookup(ip)
	if !ok {
		return nil, ErrUnknownLocation
	}
	d := &Decision{ClientIP: ip, ClientLoc: loc}

	// 跨国流量精准拦截：来源国与网关服务国不一致，直接降级，不进入选路。
	if loc.Country != g.homeCountry {
		d.Degraded = true
		d.Reason = fmt.Sprintf("origin country %q != home %q", loc.Country, g.homeCountry)
		d.Elapsed = time.Since(start)
		return d, ErrCrossBorder
	}

	// 1) 同局域网优先：节点声明的网段直接命中客户端 IP。
	if n := g.pickLANNode(ip); n != nil {
		d.Cluster, d.Node = n.Cluster, n
		d.DistanceKm = HaversineKm(loc, n.Loc)
		d.EstimatedRTT = rttOf(n, d.DistanceKm)
		d.Reason = "same-LAN hit"
		d.Elapsed = time.Since(start)
		return d, nil
	}

	// 2) 候选集群：本国 + 有健康节点，按 同城 > 距离 > 实测RTT 排序。
	type cand struct {
		c        *Cluster
		sameCity bool
		dist     float64
		rtt      time.Duration
	}
	var cands []cand
	for _, c := range g.clusters {
		if c.Loc.Country != g.homeCountry {
			continue // 国内流量绝不落海外集群
		}
		healthy := c.healthyNodes()
		if len(healthy) == 0 {
			continue
		}
		cd := cand{c: c, sameCity: loc.SameCity(c.Loc), dist: HaversineKm(loc, c.Loc)}
		for _, n := range healthy {
			if r := n.RTT(); r > 0 && (cd.rtt == 0 || r < cd.rtt) {
				cd.rtt = r
			}
		}
		cands = append(cands, cd)
	}
	if len(cands) == 0 {
		return nil, ErrNoHealthyNode
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].sameCity != cands[j].sameCity {
			return cands[i].sameCity
		}
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].rtt < cands[j].rtt
	})

	// 3) 集群内一致性哈希选节点；首选集群全宕时自然落到下一个就近集群，
	//    哈希环保证集群内映射扰动最小（平滑重选）。
	for _, cd := range cands {
		if n := cd.c.ring.get(clientIP); n != nil {
			d.Cluster, d.Node = cd.c.Name, n
			d.DistanceKm = cd.dist
			d.EstimatedRTT = rttOf(n, cd.dist)
			if cd.sameCity {
				d.Reason = "same-city cluster"
			} else {
				d.Reason = "nearest healthy cluster"
			}
			d.Elapsed = time.Since(start)
			return d, nil
		}
	}
	return nil, ErrNoHealthyNode
}

// pickLANNode 在声明了客户端所在网段的健康节点中做哈希选择。
func (g *Gateway) pickLANNode(ip net.IP) *Node {
	var hits []*Node
	for _, c := range g.clusters {
		if c.Loc.Country != g.homeCountry {
			continue
		}
		for _, n := range c.healthyNodes() {
			if n.ServesLAN(ip) {
				hits = append(hits, n)
			}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	return hits[crc32.ChecksumIEEE(ip)%uint32(len(hits))]
}

func rttOf(n *Node, distKm float64) time.Duration {
	if r := n.RTT(); r > 0 {
		return r
	}
	return EstimateRTT(distKm)
}

// StartProber 启动后台健康探测（默认秒级间隔），
// 用 ping 函数刷新每个节点的健康位与实测 RTT，返回停止函数。
func (g *Gateway) StartProber(interval time.Duration, ping func(*Node) (time.Duration, error)) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				for _, c := range g.clusters {
					for _, n := range c.Nodes() {
						rtt, err := ping(n)
						if err != nil {
							n.SetHealthy(false)
							continue
						}
						n.SetHealthy(true)
						n.SetRTT(rtt)
					}
				}
			}
		}
	}()
	return func() { close(done) }
}
