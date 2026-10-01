package routemig

import (
	"fmt"
	"sync"
)

// Version 标识键的权威数据所在的拓扑代际。
type Version uint8

const (
	// VersionOld 表示键归属旧拓扑 Top_Old。
	VersionOld Version = 1
	// VersionNew 表示键归属新拓扑 Top_New。
	VersionNew Version = 2
)

func (v Version) String() string {
	switch v {
	case VersionOld:
		return "Top_Old"
	case VersionNew:
		return "Top_New"
	default:
		return "Unknown"
	}
}

// Metrics 记录控制器处理的请求统计。
type Metrics struct {
	Reads     int
	Writes    int
	OldRouted int // 路由到旧拓扑的请求数
	NewRouted int // 路由到新拓扑的请求数
}

// Controller 是多版本路由拓扑渐进式迁移控制器。
//
// 工作原理：
//  1. 双拓扑共存期间，控制器维护一份“键版本目录”(key directory)，
//     记录每个键的权威数据位于哪一代拓扑。
//  2. 首次访问某个键时，按灰度百分比做确定性分桶：
//     hash(key) % 100 < grayPercent 的键划归新拓扑，其余留在旧拓扑。
//  3. 决策一旦做出即写入目录并固化，后续该键的所有读写都路由到同一
//     拓扑，从机制上杜绝双写分裂（同一键永不同时写两侧）。
//  4. 推移灰度比例只影响尚未决策的新键，已固化的键不受影响，
//     因此可以按 10% 步长平滑推进到 100%。
type Controller struct {
	mu          sync.RWMutex
	oldTopo     *Topology
	newTopo     *Topology
	grayPercent int // 0..100，新拓扑承接的流量百分比
	dir         map[string]Version
	metrics     Metrics
}

// NewController 创建仅含旧拓扑的控制器（迁移前的初始状态）。
func NewController(oldTopo *Topology) *Controller {
	return &Controller{
		oldTopo: oldTopo,
		dir:     make(map[string]Version),
	}
}

// PublishNewTopology 发布新集群配置，进入双拓扑共存状态，灰度从 0% 开始。
func (c *Controller) PublishNewTopology(newTopo *Topology) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.newTopo = newTopo
	c.grayPercent = 0
}

// SetGrayPercent 设定新拓扑承接的流量百分比（0..100）。
func (c *Controller) SetGrayPercent(p int) error {
	if p < 0 || p > 100 {
		return fmt.Errorf("gray percent %d out of range [0,100]", p)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.newTopo == nil && p > 0 {
		return fmt.Errorf("new topology not published yet")
	}
	c.grayPercent = p
	return nil
}

// StepUp 以给定步长（如 10）上调灰度比例，返回调整后的比例。
func (c *Controller) StepUp(step int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.newTopo == nil {
		return 0, fmt.Errorf("new topology not published yet")
	}
	if step <= 0 {
		return c.grayPercent, fmt.Errorf("step must be positive, got %d", step)
	}
	c.grayPercent += step
	if c.grayPercent > 100 {
		c.grayPercent = 100
	}
	return c.grayPercent, nil
}

// GrayPercent 返回当前灰度比例。
func (c *Controller) GrayPercent() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.grayPercent
}

// routeVersion 决定键的路由代际：已固化的直接返回；未固化的按灰度分桶并固化。
func (c *Controller) routeVersion(key string) Version {
	c.mu.RLock()
	if v, ok := c.dir[key]; ok {
		c.mu.RUnlock()
		return v
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.dir[key]; ok { // double-check
		return v
	}
	v := VersionOld
	if c.newTopo != nil && int(hash64(key)%100) < c.grayPercent {
		v = VersionNew
	}
	c.dir[key] = v
	return v
}

// topologyOf 返回代际对应的拓扑。
func (c *Controller) topologyOf(v Version) *Topology {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if v == VersionNew {
		return c.newTopo
	}
	return c.oldTopo
}

// Write 将键值写入其版本归属的拓扑，返回命中的节点 ID 与代际。
func (c *Controller) Write(key, value string) (string, Version) {
	v := c.routeVersion(key)
	node := c.topologyOf(v).Locate(key)
	node.Put(key, value)
	c.mu.Lock()
	c.metrics.Writes++
	if v == VersionNew {
		c.metrics.NewRouted++
	} else {
		c.metrics.OldRouted++
	}
	c.mu.Unlock()
	return node.ID, v
}

// Read 从键的版本归属拓扑读取，返回 (值, 是否存在, 代际)。
// 读取未写入过的键同样会固化其路由代际，保证后续写不漂移。
func (c *Controller) Read(key string) (string, bool, Version) {
	v := c.routeVersion(key)
	node := c.topologyOf(v).Locate(key)
	val, ok := node.Get(key)
	c.mu.Lock()
	c.metrics.Reads++
	if v == VersionNew {
		c.metrics.NewRouted++
	} else {
		c.metrics.OldRouted++
	}
	c.mu.Unlock()
	return val, ok, v
}

// KeyVersion 查询键已固化的路由代际；未固化时 ok=false。
func (c *Controller) KeyVersion(key string) (Version, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.dir[key]
	return v, ok
}

// Metrics 返回请求统计快照。
func (c *Controller) Metrics() Metrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metrics
}

// DirectorySize 返回已固化路由决策的键数量。
func (c *Controller) DirectorySize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.dir)
}

// VersionDistribution 统计目录中各代际的键数量（用于观测灰度分布）。
func (c *Controller) VersionDistribution() map[Version]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[Version]int{VersionOld: 0, VersionNew: 0}
	for _, v := range c.dir {
		out[v]++
	}
	return out
}

// NodeLoadEntry 描述单个节点承载的键数量。
type NodeLoadEntry struct {
	NodeID string
	Keys   int
}

// NodeLoad 返回指定代际拓扑中各节点承载的键数量（按节点 ID 排序）。
func (c *Controller) NodeLoad(v Version) []NodeLoadEntry {
	t := c.topologyOf(v)
	if t == nil {
		return nil
	}
	nodes := t.Nodes()
	out := make([]NodeLoadEntry, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, NodeLoadEntry{NodeID: n.ID, Keys: n.Len()})
	}
	return out
}
