// Package leasering 实现租约感知的一致性哈希寻址器。
//
// 节点通过周期性心跳续约租约；只有持有有效在线租约的节点
// 才参与哈希环拓扑。租约超时（超过 leaseTicks 个 tick 未续约）
// 的节点会在一次原子操作中被整体移出红黑树环，其虚拟节点
// 覆盖的 Key 区间瞬间透明平移给顺时针后继节点。
package leasering

import (
	"errors"
	"hash/fnv"
	"sort"
	"sync"
)

// vnode 是环上的一个虚拟节点，隶属于某个物理节点。
type vnode struct {
	hash   uint32
	nodeID string
}

// nodeState 记录一个物理节点的租约状态与其虚拟节点位置。
type nodeState struct {
	id            string
	lastHeartbeat int64 // 最近一次续约时的 tick
	hashes        []uint32
}

// RebalanceEvent 描述一次原子剔除引发的重平衡结果。
type RebalanceEvent struct {
	Tick        int64          // 发生剔除的逻辑 tick
	RemovedNode string         // 被剔除的节点
	MovedVNodes int            // 被抹去的虚拟节点数量
	Inherited   map[string]int // 顺时针后继节点 -> 继承的 Key 区间数
}

// Ring 是租约感知的一致性哈希环。
type Ring struct {
	mu            sync.RWMutex
	tree          *rbTree
	nodes         map[string]*nodeState
	vnodesPerNode int
	leaseTicks    int64
	tick          int64
	onRebalance   func(RebalanceEvent)
}

// Option 用于定制 Ring。
type Option func(*Ring)

// WithVNodes 设置每个物理节点的虚拟节点数（默认 64）。
func WithVNodes(n int) Option {
	return func(r *Ring) {
		if n > 0 {
			r.vnodesPerNode = n
		}
	}
}

// WithLeaseTicks 设置租约长度：超过 n 个 tick 未心跳即判离线（默认 3）。
func WithLeaseTicks(n int64) Option {
	return func(r *Ring) {
		if n > 0 {
			r.leaseTicks = n
		}
	}
}

// WithRebalanceHook 注册重平衡回调，在节点被原子剔除后触发。
func WithRebalanceHook(fn func(RebalanceEvent)) Option {
	return func(r *Ring) { r.onRebalance = fn }
}

// New 创建一个租约感知哈希环。
func New(opts ...Option) *Ring {
	r := &Ring{
		tree:          &rbTree{},
		nodes:         make(map[string]*nodeState),
		vnodesPerNode: 64,
		leaseTicks:    3,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ErrUnknownNode 表示目标节点不在环上。
var ErrUnknownNode = errors.New("leasering: unknown node")

func hashKey(parts ...string) uint32 {
	h := fnv.New32a()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return h.Sum32()
}

// AddNode 将节点加入环并授予当前 tick 起算的租约。
// 重复加入同一节点是幂等的（视为一次心跳续约）。
func (r *Ring) AddNode(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.nodes[id]; ok {
		st.lastHeartbeat = r.tick
		return
	}
	st := &nodeState{id: id, lastHeartbeat: r.tick}
	for i := 0; i < r.vnodesPerNode; i++ {
		h := hashKey(id, "#", itoa(i))
		for r.tree.get(h) != nil { // 哈希碰撞时线性探测
			h++
		}
		r.tree.insert(h, &vnode{hash: h, nodeID: id})
		st.hashes = append(st.hashes, h)
	}
	r.nodes[id] = st
}

// RemoveNode 主动将节点从环上剔除（不触发租约流程）。
func (r *Ring) RemoveNode(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return ErrUnknownNode
	}
	r.evictLocked(id)
	return nil
}

// Heartbeat 为节点续约租约。节点必须已在环上。
func (r *Ring) Heartbeat(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.nodes[id]
	if !ok {
		return ErrUnknownNode
	}
	st.lastHeartbeat = r.tick
	return nil
}

// Tick 将逻辑时钟推进 n 个 tick（n<=0 时推进 1 个），
// 随后在同一临界区内原子剔除所有租约过期的节点：
// 对任一观察者而言，环上要么还有该节点、要么已整体消失，
// 不存在只删掉部分虚拟节点的中间态。
func (r *Ring) Tick(n int64) []RebalanceEvent {
	if n <= 0 {
		n = 1
	}
	r.mu.Lock()
	r.tick += n
	var expired []string
	for id, st := range r.nodes {
		if r.tick-st.lastHeartbeat > r.leaseTicks {
			expired = append(expired, id)
		}
	}
	sort.Strings(expired) // 保证多节点同时过期时事件顺序确定
	var events []RebalanceEvent
	for _, id := range expired {
		events = append(events, r.evictLocked(id))
	}
	hook := r.onRebalance
	r.mu.Unlock()
	if hook != nil {
		for _, ev := range events {
			hook(ev)
		}
	}
	return events
}

// evictLocked 在持锁状态下原子抹去节点的全部虚拟节点，
// 并统计每个 Key 区间的顺时针继承者。调用方必须持有写锁。
func (r *Ring) evictLocked(id string) RebalanceEvent {
	st := r.nodes[id]
	ev := RebalanceEvent{
		Tick:        r.tick,
		RemovedNode: id,
		MovedVNodes: len(st.hashes),
		Inherited:   make(map[string]int),
	}
	// 先整体抹去全部虚拟节点，再统计继承者，
	// 避免把区间算到尚未删除的自家虚拟节点头上。
	for _, h := range st.hashes {
		r.tree.delete(r.tree.get(h))
	}
	for _, h := range st.hashes {
		// 该虚拟节点原来覆盖的 Key 区间，现在归顺时针后继所有。
		if succ := r.clockwiseLocked(h); succ != "" {
			ev.Inherited[succ]++
		}
	}
	delete(r.nodes, id)
	return ev
}

// clockwiseLocked 返回哈希 h 在环上的顺时针归属节点（含回绕）。
func (r *Ring) clockwiseLocked(h uint32) string {
	n := r.tree.ceiling(h)
	if n == nil {
		n = r.tree.min()
	}
	if n == nil {
		return ""
	}
	return n.vnode.nodeID
}

// Locate 返回 key 的归属节点：沿环顺时针找到第一个
// 持有有效租约的虚拟节点。环为空时返回 ErrUnknownNode。
func (r *Ring) Locate(key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner := r.clockwiseLocked(hashKey(key))
	if owner == "" {
		return "", ErrUnknownNode
	}
	return owner, nil
}

// Nodes 返回当前持有有效租约、参与环拓扑的节点列表（已排序）。
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// VNodeCount 返回环上虚拟节点总数，用于校验拓扑完整性。
func (r *Ring) VNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tree.len()
}

// TickNow 返回当前逻辑 tick。
func (r *Ring) TickNow() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tick
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
