package routemig

import (
	"fmt"
	"sort"
	"sync"
)

// Node 表示集群中的一个存储节点。
type Node struct {
	ID   string
	Addr string

	mu    sync.RWMutex
	items map[string]string
}

// NewNode 创建一个空节点。
func NewNode(id, addr string) *Node {
	return &Node{ID: id, Addr: addr, items: make(map[string]string)}
}

// Put 写入键值。
func (n *Node) Put(key, value string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.items[key] = value
}

// Get 读取键值。
func (n *Node) Get(key string) (string, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	v, ok := n.items[key]
	return v, ok
}

// Len 返回节点上的键数量（用于观测数据分布）。
func (n *Node) Len() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.items)
}

// ClusterConfig 描述一次发布的集群配置。
type ClusterConfig struct {
	Name  string
	Nodes []NodeSpec
}

// NodeSpec 是节点的静态描述。
type NodeSpec struct {
	ID   string
	Addr string
}

// Topology 是一个版本化的路由拓扑，内部使用一致性哈希环定位节点。
type Topology struct {
	name    string
	nodes   map[string]*Node
	ring    []uint64         // 排序的虚拟节点哈希
	virtMap map[uint64]*Node // 虚拟节点 -> 物理节点
	replica int
}

const defaultReplicas = 128

// NewTopology 根据集群配置构建拓扑。
func NewTopology(cfg ClusterConfig) (*Topology, error) {
	if len(cfg.Nodes) == 0 {
		return nil, fmt.Errorf("topology %q: empty cluster config", cfg.Name)
	}
	t := &Topology{
		name:    cfg.Name,
		nodes:   make(map[string]*Node, len(cfg.Nodes)),
		virtMap: make(map[uint64]*Node, len(cfg.Nodes)*defaultReplicas),
		replica: defaultReplicas,
	}
	for _, spec := range cfg.Nodes {
		if _, dup := t.nodes[spec.ID]; dup {
			return nil, fmt.Errorf("topology %q: duplicate node id %q", cfg.Name, spec.ID)
		}
		n := NewNode(spec.ID, spec.Addr)
		t.nodes[spec.ID] = n
		for i := 0; i < t.replica; i++ {
			h := hash64(fmt.Sprintf("%s#%d", spec.ID, i))
			t.ring = append(t.ring, h)
			t.virtMap[h] = n
		}
	}
	sort.Slice(t.ring, func(i, j int) bool { return t.ring[i] < t.ring[j] })
	return t, nil
}

// Name 返回拓扑名称。
func (t *Topology) Name() string { return t.name }

// Nodes 返回拓扑中的全部节点。
func (t *Topology) Nodes() []*Node {
	out := make([]*Node, 0, len(t.nodes))
	for _, n := range t.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Locate 按一致性哈希定位键所属节点。
func (t *Topology) Locate(key string) *Node {
	h := hash64(key)
	i := sort.Search(len(t.ring), func(i int) bool { return t.ring[i] >= h })
	if i == len(t.ring) {
		i = 0
	}
	return t.virtMap[t.ring[i]]
}

// hash64 使用 FNV-1a 64 位散列。
func hash64(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}
