// Package lease 实现多节点租约竞争与脑裂预防仲裁器。
//
// 协议要点（多数派租约仲裁）：
//  1. 候选者必须获得集群超过半数（Quorum）节点的租约授权，才可成为有效 Leader；
//  2. 每个节点在租约未过期前，最多将授权授予一个候选者（授权互斥），
//     因此任意时刻至多一个 Leader 持有有效租约，杜绝脑裂；
//  3. Leader 的租约有效期取法定多数授权中最早的过期时间；
//  4. 租约过期后主权限被自动剥离，节点不再响应写请求；
//  5. 网络分区时，少数派分区无法凑齐多数派授权，租约到期后自动退位。
package lease

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrNotLeader 表示节点当前不是有效 Leader。
	ErrNotLeader = errors.New("not leader: write rejected")
	// ErrNoQuorum 表示无法获得多数派租约授权。
	ErrNoQuorum = errors.New("no quorum: majority lease grants unavailable")
)

// Clock 抽象时钟，测试可注入 FakeClock 以获得确定性。
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// FakeClock 是手动推进的时钟，用于确定性测试。
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Node 是集群中的一个节点。
type Node struct {
	id            int
	cluster       *Cluster
	clock         Clock
	leaseDuration time.Duration

	mu sync.Mutex

	term int // 当前任期（单调递增，用于拒绝过期请求）

	// 本节点作为授权方的状态
	grantHolder int       // 本节点当前授权给了哪个候选者（0 = 无）
	grantExpiry time.Time // 本节点授权的过期时间

	// 本节点作为 Leader 的状态
	leader            bool
	leaderLeaseExpiry time.Time // 法定多数授权中最早的过期时间
	log               []string  // 已接受的写请求（演示用）
}

func (n *Node) ID() int { return n.id }

// Term 返回节点当前任期。
func (n *Node) Term() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.term
}

// IsLeader 报告节点当前是否持有有效租约。
// 租约过期时在此惰性剥离主权限。
func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leaderLocked()
}

func (n *Node) leaderLocked() bool {
	if n.leader && !n.clock.Now().Before(n.leaderLeaseExpiry) {
		n.leader = false // 租约过期：自动剥离主权限
	}
	return n.leader
}

// Elect 发起新一轮选举：任期 +1，向全集群请求租约授权。
func (n *Node) Elect() error {
	n.mu.Lock()
	term := n.term + 1
	n.mu.Unlock()
	return n.acquire(term)
}

// Renew 由现任 Leader 续订租约（任期不变）。
// 续订失败不会立即剥夺主权限——既有租约在过期前仍然有效。
func (n *Node) Renew() error {
	n.mu.Lock()
	if !n.leaderLocked() {
		n.mu.Unlock()
		return ErrNotLeader
	}
	term := n.term
	n.mu.Unlock()
	return n.acquire(term)
}

// acquire 向集群中所有可达节点请求租约授权，
// 获得超过半数授权后成为/续任 Leader。
func (n *Node) acquire(term int) error {
	grants, minExpiry := n.cluster.collectGrants(n.id, term, n.leaseDuration)
	if grants <= n.cluster.Size()/2 {
		return fmt.Errorf("%w (grants=%d, need>%d)", ErrNoQuorum, grants, n.cluster.Size()/2)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.term = term
	n.leader = true
	n.leaderLeaseExpiry = minExpiry
	return nil
}

// Write 仅当节点持有有效租约时接受写请求，否则拒绝。
func (n *Node) Write(cmd string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.leaderLocked() {
		return ErrNotLeader
	}
	n.log = append(n.log, cmd)
	return nil
}

// Log 返回已接受的写请求副本。
func (n *Node) Log() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.log...)
}

// handleRequestLease 是授权方逻辑：在租约未过期前只授权给一个候选者。
func (n *Node) handleRequestLease(candidateID, term int, dur time.Duration) (bool, time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clock.Now()
	if term < n.term {
		return false, time.Time{} // 过期任期，拒绝
	}
	if n.grantHolder != 0 && n.grantHolder != candidateID && now.Before(n.grantExpiry) {
		return false, time.Time{} // 租约仍授予他人，拒绝（授权互斥）
	}
	if term > n.term {
		n.term = term
	}
	n.grantHolder = candidateID
	n.grantExpiry = now.Add(dur)
	return true, n.grantExpiry
}

// Cluster 模拟集群网络，支持构造/恢复网络分区。
type Cluster struct {
	mu    sync.RWMutex
	ids   []int
	nodes map[int]*Node
	links map[int]map[int]bool // links[a][b] = a 与 b 之间可通信
}

// NewCluster 创建 size 个节点、全连通的集群。
func NewCluster(size int, leaseDuration time.Duration, clock Clock) *Cluster {
	if clock == nil {
		clock = realClock{}
	}
	c := &Cluster{
		nodes: make(map[int]*Node),
		links: make(map[int]map[int]bool),
	}
	for i := 1; i <= size; i++ {
		c.ids = append(c.ids, i)
		c.nodes[i] = &Node{
			id:            i,
			cluster:       c,
			clock:         clock,
			leaseDuration: leaseDuration,
		}
	}
	c.Heal()
	return c
}

// Size 返回集群节点总数。
func (c *Cluster) Size() int { return len(c.ids) }

// Node 返回指定 ID 的节点。
func (c *Cluster) Node(id int) *Node { return c.nodes[id] }

// Heal 恢复全连通。
func (c *Cluster) Heal() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.links = make(map[int]map[int]bool)
	for _, a := range c.ids {
		c.links[a] = make(map[int]bool)
		for _, b := range c.ids {
			if a != b {
				c.links[a][b] = true
			}
		}
	}
}

// Partition 按给定分组构造网络分区：组内连通，组间隔离。
func (c *Cluster) Partition(groups ...[]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.links = make(map[int]map[int]bool)
	for _, a := range c.ids {
		c.links[a] = make(map[int]bool)
	}
	for _, group := range groups {
		for _, a := range group {
			for _, b := range group {
				if a != b {
					c.links[a][b] = true
				}
			}
		}
	}
}

func (c *Cluster) reachable(a, b int) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return a == b || c.links[a][b]
}

// collectGrants 代表候选者向所有可达节点（含自身）请求租约授权，
// 返回授权数量与所有授权中最早的过期时间。
func (c *Cluster) collectGrants(candidateID, term int, dur time.Duration) (int, time.Time) {
	grants := 0
	var minExpiry time.Time
	for _, id := range c.ids {
		if !c.reachable(candidateID, id) {
			continue // 分区隔离，消息丢失
		}
		ok, expiry := c.nodes[id].handleRequestLease(candidateID, term, dur)
		if ok {
			grants++
			if minExpiry.IsZero() || expiry.Before(minExpiry) {
				minExpiry = expiry
			}
		}
	}
	return grants, minExpiry
}
