// Package lease 实现主从选举中的多数派租约仲裁协议。
//
// 核心规则：
//   - 节点必须获得集群超过半数（Quorum = N/2+1）节点的租约授权，才能成为有效 Leader；
//   - 每个授权节点在同一任期内只把租约授予一个候选者，且授权带 TTL；
//   - Leader 必须在租约到期前续租（重新收集 Quorum 授权），否则租约过期自动剥离主权限；
//   - 网络对称/非对称分区时，少数派分区节点无法凑齐 Quorum 授权，
//     租约过期后自动退位，不再响应写请求，从而杜绝脑裂。
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNotLeader 表示当前节点不持有有效多数派租约，拒绝写请求。
var ErrNotLeader = errors.New("lease: not a leader with a valid quorum lease")

// NodeID 是集群内节点的唯一标识。
type NodeID int

// NoNode 表示“无节点”。
const NoNode NodeID = -1

// Cluster 维护节点集合、Quorum 大小以及可模拟的网络分区（支持非对称断链）。
type Cluster struct {
	mu     sync.RWMutex
	nodes  []*Node
	quorum int
	// blocked[from][to] == true 表示 from 发往 to 的消息不可达（单向）。
	blocked map[NodeID]map[NodeID]bool
}

// NewCluster 创建包含 size 个节点的集群。
func NewCluster(size int) *Cluster {
	if size < 1 {
		panic("lease: cluster size must be >= 1")
	}
	c := &Cluster{
		quorum:  size/2 + 1,
		blocked: make(map[NodeID]map[NodeID]bool),
	}
	for i := 0; i < size; i++ {
		c.nodes = append(c.nodes, newNode(NodeID(i), c))
	}
	return c
}

// Node 返回指定 ID 的节点。
func (c *Cluster) Node(id NodeID) *Node { return c.nodes[int(id)] }

// Nodes 返回全部节点。
func (c *Cluster) Nodes() []*Node { return c.nodes }

// Quorum 返回多数派阈值（N/2+1）。
func (c *Cluster) Quorum() int { return c.quorum }

// Size 返回集群节点数。
func (c *Cluster) Size() int { return len(c.nodes) }

// Partition 将集群对称地划分为 groupA 与其余节点两个互不可达的分区。
func (c *Cluster) Partition(groupA ...NodeID) {
	inA := make(map[NodeID]bool, len(groupA))
	for _, id := range groupA {
		inA[id] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.nodes {
		for _, b := range c.nodes {
			if a.id == b.id {
				continue
			}
			if inA[a.id] != inA[b.id] {
				c.setBlockedLocked(a.id, b.id, true)
			}
		}
	}
}

// BlockLink 断开 from -> to 的单向链路（用于模拟非对称分区）。
func (c *Cluster) BlockLink(from, to NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setBlockedLocked(from, to, true)
}

// Heal 恢复所有网络链路。
func (c *Cluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = make(map[NodeID]map[NodeID]bool)
}

func (c *Cluster) setBlockedLocked(from, to NodeID, v bool) {
	m := c.blocked[from]
	if m == nil {
		m = make(map[NodeID]bool)
		c.blocked[from] = m
	}
	m[to] = v
}

// reachable 判断 from 到 to 的单向可达性。
func (c *Cluster) reachable(from, to NodeID) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.blocked[from][to]
}

// rpcOK 判断一次请求-响应 RPC 是否可完成（双向均需可达）。
func (c *Cluster) rpcOK(a, b NodeID) bool {
	return c.reachable(a, b) && c.reachable(b, a)
}

// Shutdown 停止所有节点的后台续租协程。
func (c *Cluster) Shutdown() {
	for _, n := range c.nodes {
		n.Shutdown()
	}
}

// Node 是集群成员，持有授权状态与（若为 Leader）自身租约。
type Node struct {
	id      NodeID
	cluster *Cluster

	mu           sync.Mutex
	term         int64     // 本节点见过的最大任期
	grantedTo    NodeID    // 当前租约授予对象（作为授权方）
	grantedUntil time.Time // 授权租约到期时刻

	leader      bool
	leaseTerm   int64     // 作为 Leader 持有租约的任期
	leaseExpiry time.Time // 作为 Leader 的租约到期时刻

	stopCh  chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

func newNode(id NodeID, c *Cluster) *Node {
	return &Node{id: id, cluster: c, grantedTo: NoNode}
}

// ID 返回节点标识。
func (n *Node) ID() NodeID { return n.id }

// handleRequestLease 是租约授权 RPC 的服务端逻辑。
// 仅当 term 不落后、且当前未把未过期租约授予他人时才授权。
func (n *Node) handleRequestLease(from NodeID, term int64, ttl time.Duration) (time.Time, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	if term < n.term {
		return time.Time{}, false
	}
	if n.grantedTo != NoNode && n.grantedTo != from && now.Before(n.grantedUntil) {
		return time.Time{}, false // 租约仍被他人持有
	}
	n.term = term
	n.grantedTo = from
	n.grantedUntil = now.Add(ttl)
	return n.grantedUntil, true
}

// collectGrants 向所有可达节点（含自身）请求租约授权，
// 返回授权数量与所有授权中最早的到期时刻。
func (n *Node) collectGrants(term int64, ttl time.Duration) (int, time.Time) {
	grants := 0
	minExpiry := time.Now().Add(ttl)
	for _, peer := range n.cluster.nodes {
		if !n.cluster.rpcOK(n.id, peer.id) {
			continue
		}
		exp, ok := peer.handleRequestLease(n.id, term, ttl)
		if ok {
			grants++
			if exp.Before(minExpiry) {
				minExpiry = exp
			}
		}
	}
	return grants, minExpiry
}

// Campaign 发起竞选：提升任期并收集 Quorum 租约授权。
// 成功则成为 Leader 并启动后台续租；失败返回 false。
func (n *Node) Campaign(ttl time.Duration) bool {
	n.mu.Lock()
	n.term++
	term := n.term
	n.mu.Unlock()

	grants, expiry := n.collectGrants(term, ttl)
	if grants < n.cluster.quorum {
		return false
	}

	n.mu.Lock()
	n.leader = true
	n.leaseTerm = term
	n.leaseExpiry = expiry
	n.mu.Unlock()

	n.startRenewLoop(ttl)
	return true
}

// startRenewLoop 启动后台续租协程，周期为 ttl/3。
func (n *Node) startRenewLoop(ttl time.Duration) {
	n.stopRenew()
	n.mu.Lock()
	n.stopCh = make(chan struct{})
	n.stopped = false
	stop := n.stopCh
	n.mu.Unlock()

	interval := ttl / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				n.renewOnce(ttl)
			}
		}
	}()
}

// renewOnce 执行一次续租检查：
// 租约已过期则退位；否则尝试重新收集 Quorum 授权以延长租约。
// 收集失败不立即退位，但租约到期时刻不变，到期后自动剥离主权限。
func (n *Node) renewOnce(ttl time.Duration) {
	n.mu.Lock()
	if !n.leader {
		n.mu.Unlock()
		return
	}
	term := n.leaseTerm
	expiry := n.leaseExpiry
	n.mu.Unlock()

	if !time.Now().Before(expiry) {
		n.stepDown()
		return
	}
	grants, newExpiry := n.collectGrants(term, ttl)
	if grants >= n.cluster.quorum {
		n.mu.Lock()
		if n.leader && n.leaseTerm == term {
			n.leaseExpiry = newExpiry
		}
		n.mu.Unlock()
	}
}

// stepDown 放弃 Leader 身份并通知续租协程退出（可在续租协程内安全调用）。
func (n *Node) stepDown() {
	n.mu.Lock()
	n.leader = false
	n.stopRenewLocked()
	n.mu.Unlock()
}

// stopRenew 通知续租协程退出并等待其结束（不可在续租协程内调用）。
func (n *Node) stopRenew() {
	n.mu.Lock()
	n.stopRenewLocked()
	n.mu.Unlock()
	n.wg.Wait()
}

// stopRenewLocked 关闭续租协程的停止信号（调用者须持有 n.mu）。
func (n *Node) stopRenewLocked() {
	if n.stopCh != nil && !n.stopped {
		close(n.stopCh)
		n.stopped = true
	}
}

// Shutdown 退位并等待后台协程退出。
func (n *Node) Shutdown() {
	n.stepDown()
	n.wg.Wait()
}

// IsLeader 报告节点当前是否持有未过期的多数派租约。
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leader && time.Now().Before(n.leaseExpiry)
}

// Term 返回节点当前任期。
func (n *Node) Term() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.term
}

// LeaseExpiry 返回 Leader 租约到期时刻（非 Leader 时为零值）。
func (n *Node) LeaseExpiry() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.leader {
		return time.Time{}
	}
	return n.leaseExpiry
}

// Write 模拟写请求：只有持有有效多数派租约的 Leader 才接受写。
func (n *Node) Write(key string, value interface{}) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.leader || !time.Now().Before(n.leaseExpiry) {
		return ErrNotLeader
	}
	return nil
}

func (n *Node) String() string {
	state := "follower"
	if n.IsLeader() {
		state = "LEADER"
	}
	return fmt.Sprintf("node-%d(term=%d,%s)", n.id, n.Term(), state)
}
