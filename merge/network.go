package merge

import (
	"sync"
	"sync/atomic"
)

// Network 是进程内网络模拟器：按分区投递消息。
// 跨分区的消息被静默丢弃（模拟网络分裂），Heal 之后恢复投递。
type Network struct {
	mu    sync.RWMutex
	parts map[uint64]int   // nodeID -> partition index
	nodes map[uint64]*Node // nodeID -> node

	delivered atomic.Uint64
	dropped   atomic.Uint64
}

// NewNetwork 创建空网络。
func NewNetwork() *Network {
	return &Network{
		parts: make(map[uint64]int),
		nodes: make(map[uint64]*Node),
	}
}

// attach 将节点注册进网络（由 NewNode 调用）。
func (n *Network) attach(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.ID] = node
	// 默认与自身同分区（未显式分区前全网互通）。
	if _, ok := n.parts[node.ID]; !ok {
		n.parts[node.ID] = 0
	}
}

// SetPartition 将各组节点划入相互隔离的分区，组间消息全部丢弃。
func (n *Network) SetPartition(groups ...[]uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i, g := range groups {
		for _, id := range g {
			n.parts[id] = i + 1
		}
	}
}

// Heal 愈合网络：所有节点回到同一分区。
func (n *Network) Heal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for id := range n.parts {
		n.parts[id] = 0
	}
}

// Send 尝试投递一条消息。跨分区或对端不存在时丢弃并返回 false。
// 投递是非阻塞的（inbox 满则丢弃），保证发送方永不阻塞，避免级联死锁。
func (n *Network) Send(env Envelope) bool {
	n.mu.RLock()
	pf, okF := n.parts[env.From]
	pt, okT := n.parts[env.To]
	target := n.nodes[env.To]
	n.mu.RUnlock()

	if !okF || !okT || target == nil || pf != pt {
		n.dropped.Add(1)
		return false
	}
	select {
	case target.inbox <- env:
		n.delivered.Add(1)
		return true
	default:
		n.dropped.Add(1)
		return false
	}
}

// Stats 返回 (投递数, 丢弃数)，用于测试断言分区隔离生效。
func (n *Network) Stats() (delivered, dropped uint64) {
	return n.delivered.Load(), n.dropped.Load()
}
