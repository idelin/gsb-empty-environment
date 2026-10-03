package memberlist

import (
	"sync"
	"time"
)

// Cluster 是带标签与元数据属性的异构节点成员列表。
// 元数据并发更新通过互斥锁保护，多副本仲裁通过 Version 比较完成：
// 只接受版本更新的元数据，旧版本（含过期 gossip 消息）一律丢弃。
type Cluster struct {
	mu     sync.RWMutex
	selfID string
	nodes  map[string]*Node
	now    func() time.Time
}

// NewCluster 创建成员列表并注册本节点。
func NewCluster(selfID, selfAddr string, meta Meta, labels map[string]string) *Cluster {
	c := &Cluster{
		selfID: selfID,
		nodes:  make(map[string]*Node),
		now:    time.Now,
	}
	self := &Node{
		ID:     selfID,
		Addr:   selfAddr,
		Labels: labels,
		Meta:   meta,
		Version: Version{
			Epoch: uint64(c.now().Unix()),
			Seq:   1,
		},
		Status:   StatusAlive,
		LastSeen: c.now(),
	}
	c.nodes[selfID] = self
	return c
}

// SelfID 返回本节点 ID。
func (c *Cluster) SelfID() string { return c.selfID }

// Self 返回本节点快照。
func (c *Cluster) Self() Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nodes[c.selfID].Clone()
}

// UpdateMeta 并发安全地更新本节点元数据，并递增版本号赢得仲裁。
func (c *Cluster) UpdateMeta(m Meta) Version {
	c.mu.Lock()
	defer c.mu.Unlock()
	self := c.nodes[c.selfID]
	self.Meta = m
	self.Version = c.nextVersionLocked(self.Version)
	return self.Version
}

// SetLabel 并发安全地设置本节点标签，并递增版本号。
func (c *Cluster) SetLabel(key, value string) Version {
	c.mu.Lock()
	defer c.mu.Unlock()
	self := c.nodes[c.selfID]
	if self.Labels == nil {
		self.Labels = make(map[string]string)
	}
	self.Labels[key] = value
	self.Version = c.nextVersionLocked(self.Version)
	return self.Version
}

// nextVersionLocked 生成下一个版本：Epoch 取当前时间，若未前进则 Seq+1。
func (c *Cluster) nextVersionLocked(v Version) Version {
	epoch := uint64(c.now().Unix())
	if epoch > v.Epoch {
		return Version{Epoch: epoch, Seq: v.Seq + 1}
	}
	return Version{Epoch: v.Epoch, Seq: v.Seq + 1}
}

// Heartbeat 递增本节点心跳，供故障检测使用。
func (c *Cluster) Heartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	self := c.nodes[c.selfID]
	self.Heartbeat++
	self.LastSeen = c.now()
}

// Merge 合并 gossip 收到的成员视图，按版本号仲裁，返回是否有变更。
func (c *Cluster) Merge(remote []Node) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := false
	now := c.now()
	for i := range remote {
		rn := remote[i]
		if rn.ID == "" {
			continue
		}
		local, ok := c.nodes[rn.ID]
		if !ok {
			n := rn.Clone()
			n.LastSeen = now
			n.Status = StatusAlive
			c.nodes[rn.ID] = &n
			changed = true
			continue
		}
		// 版本仲裁：仅当远端元数据版本更新时才覆盖本地。
		if rn.Version.Compare(local.Version) > 0 {
			local.Meta = rn.Meta
			local.Labels = rn.Clone().Labels
			local.Addr = rn.Addr
			local.Version = rn.Version
			changed = true
		}
		// 心跳取较大值；心跳前进说明对端存活。
		if rn.Heartbeat > local.Heartbeat {
			local.Heartbeat = rn.Heartbeat
			local.LastSeen = now
			if local.Status != StatusAlive {
				local.Status = StatusAlive
			}
			changed = true
		}
	}
	return changed
}

// SweepDead 将心跳超时未前进的节点标记为 Dead。
func (c *Cluster) SweepDead(timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for id, n := range c.nodes {
		if id == c.selfID {
			continue
		}
		if n.Status == StatusAlive && now.Sub(n.LastSeen) > timeout {
			n.Status = StatusDead
		}
	}
}

// Snapshot 返回全部成员的一致性快照（深拷贝）。
func (c *Cluster) Snapshot() []Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Node, 0, len(c.nodes))
	for _, n := range c.nodes {
		out = append(out, n.Clone())
	}
	return out
}

// Get 按 ID 查询节点快照。
func (c *Cluster) Get(id string) (Node, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.nodes[id]
	if !ok {
		return Node{}, false
	}
	return n.Clone(), true
}
