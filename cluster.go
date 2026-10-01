package graceful

import (
	"sync"
	"time"
)

// Cluster 维护节点集合，负责 Join/Leave 广播与节点间 RPC 路由。
type Cluster struct {
	mu        sync.Mutex
	nodes     map[string]*Node
	quiescent time.Duration
}

// NewCluster 创建集群，quiescent 为各节点路由表使用的平静期时长。
func NewCluster(quiescent time.Duration) *Cluster {
	return &Cluster{
		nodes:     make(map[string]*Node),
		quiescent: quiescent,
	}
}

// Join 将新节点加入集群：与既有节点互相建立路由。
func (c *Cluster) Join(id string) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := newNode(id, c.quiescent)
	n.cluster = c
	for _, peer := range c.nodes {
		peer.Router.Add(id)
		n.Router.Add(peer.ID)
	}
	c.nodes[id] = n
	return n
}

// broadcastLeave 将离开意向广播给所有其他节点：
// 各节点路由表将目标置为 Leaving 并启动平静期定时器。
func (c *Cluster) broadcastLeave(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, peer := range c.nodes {
		if peer.ID == id {
			continue
		}
		peer.Router.MarkLeaving(id)
	}
}

// Call 从 from 向 to 发起一次 RPC。
// 路由决策在调用方本地完成：目标不在就绪池时立即失败（ErrNotReady），
// 不会产生突发 RPC 超时；目标在平静期内拒绝新流量（ErrDraining），
// 已在途的请求正常执行完成。
func (c *Cluster) Call(from, to string, work func()) error {
	c.mu.Lock()
	src, okSrc := c.nodes[from]
	dst, okDst := c.nodes[to]
	c.mu.Unlock()
	if !okSrc || !okDst {
		return ErrGone
	}
	if !src.Router.IsReady(to) {
		return ErrNotReady
	}
	return dst.serve(work)
}

// Node 按 ID 返回节点。
func (c *Cluster) Node(id string) (*Node, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, ok := c.nodes[id]
	return n, ok
}
