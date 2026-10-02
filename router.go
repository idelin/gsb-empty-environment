// Package leasehash 实现租约感知的一致性哈希寻址器。
//
// 路由组件将节点心跳租约状态与哈希环（红黑树）绑定：
// 只有持有有效在线租约的节点才参与哈希环拓扑；
// 当某节点心跳超时掉线时，寻址器原子地将其全部虚拟节点从环上剔除，
// 原属于该节点的 Key 依据一致性哈希语义瞬间透明平移给顺时针下一节点，
// 业务无阻断。
package leasehash

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
)

// ErrNodeExists 节点已存在。
var ErrNodeExists = errors.New("leasehash: node already exists")

// ErrNodeNotFound 节点不存在。
var ErrNodeNotFound = errors.New("leasehash: node not found")

// ErrNoOnlineNode 环上没有任何在线节点。
var ErrNoOnlineNode = errors.New("leasehash: no online node in ring")

// DownEvent 描述一次节点掉线引发的原子重平衡。
type DownEvent struct {
	NodeID        string   // 掉线节点
	VNodesRemoved int      // 从环上抹去的虚拟节点数
	Successors    []string // 每个虚拟节点顺时针方向的接管者（按哈希升序）
}

// nodeState 记录一个物理节点的租约与虚拟节点。
type nodeState struct {
	id        string
	vnodes    []uint64 // 该节点在环上的全部虚拟节点哈希
	leaseLeft int      // 剩余租约（单位：tick），>0 表示在线
}

// Router 是租约感知的一致性哈希寻址器。对并发安全。
type Router struct {
	mu       sync.RWMutex
	tree     rbTree
	nodes    map[string]*nodeState
	replicas int // 每节点虚拟节点数
	leaseTTL int // 租约时长（单位：tick）

	// OnNodeDown 在节点被原子剔除出环后触发（在锁外回调）。
	// 下游可借此执行重平衡（如失效本地缓存、迁移会话）。
	OnNodeDown func(ev DownEvent)
}

// NewRouter 创建寻址器。replicas 为每节点虚拟节点数，leaseTTLTicks 为租约 tick 数。
func NewRouter(replicas, leaseTTLTicks int) *Router {
	if replicas <= 0 {
		replicas = 128
	}
	if leaseTTLTicks <= 0 {
		leaseTTLTicks = 3
	}
	return &Router{
		nodes:    make(map[string]*nodeState),
		replicas: replicas,
		leaseTTL: leaseTTLTicks,
	}
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// vnodeHash 计算节点 id 的第 i 个虚拟节点哈希。
func vnodeHash(id string, i int) uint64 {
	return hash64(fmt.Sprintf("%s#vnode-%d", id, i))
}

// AddNode 将节点加入哈希环并授予完整租约。
func (r *Router) AddNode(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; ok {
		return ErrNodeExists
	}
	st := &nodeState{id: id, leaseLeft: r.leaseTTL}
	for i := 0; i < r.replicas; i++ {
		h := vnodeHash(id, i)
		// 哈希冲突时线性探测，保证虚拟节点不丢失。
		for {
			if _, taken := r.tree.get(h); !taken {
				break
			}
			h++
		}
		r.tree.insert(h, id)
		st.vnodes = append(st.vnodes, h)
	}
	r.nodes[id] = st
	return nil
}

// removeLocked 原子地将节点的全部虚拟节点从环上抹去。调用方须持有写锁。
func (r *Router) removeLocked(st *nodeState) DownEvent {
	ev := DownEvent{NodeID: st.id}
	// 第一阶段：在同一把写锁内抹去全部虚拟节点（原子拓扑变更）。
	for _, h := range st.vnodes {
		if r.tree.remove(h) {
			ev.VNodesRemoved++
		}
	}
	// 第二阶段：基于剔除后的完整环，计算每个虚拟节点位置的顺时针接管者。
	for _, h := range st.vnodes {
		if succ, ok := r.tree.successor(h); ok {
			ev.Successors = append(ev.Successors, succ.value)
		}
	}
	delete(r.nodes, st.id)
	return ev
}

// RemoveNode 主动摘除节点（等价于租约立即失效），原子剔除并触发重平衡回调。
func (r *Router) RemoveNode(id string) error {
	r.mu.Lock()
	st, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return ErrNodeNotFound
	}
	ev := r.removeLocked(st)
	cb := r.OnNodeDown
	r.mu.Unlock()
	if cb != nil {
		cb(ev)
	}
	return nil
}

// Heartbeat 续租：将节点租约重置为完整 TTL。节点不存在时返回 ErrNodeNotFound。
func (r *Router) Heartbeat(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	st.leaseLeft = r.leaseTTL
	return nil
}

// Tick 推进一个心跳周期：所有节点租约减一；
// 租约耗尽的节点被判定掉线，其虚拟节点在同一把写锁内原子地从红黑树环上抹去，
// 随后触发下游重平衡回调。返回本次掉线事件列表。
func (r *Router) Tick() []DownEvent {
	r.mu.Lock()
	var events []DownEvent
	for _, st := range r.nodes {
		st.leaseLeft--
		if st.leaseLeft <= 0 {
			events = append(events, r.removeLocked(st))
		}
	}
	cb := r.OnNodeDown
	r.mu.Unlock()
	if cb != nil {
		for _, ev := range events {
			cb(ev)
		}
	}
	return events
}

// Lookup 返回 key 应路由到的在线节点。
// 只有持有有效租约、当前在环上的节点才可能被返回。
func (r *Router) Lookup(key string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.tree.len() == 0 {
		return "", ErrNoOnlineNode
	}
	succ, _ := r.tree.successor(hash64(key))
	return succ.value, nil
}

// IsOnline 报告节点当前是否持有有效租约（即在环上）。
func (r *Router) IsOnline(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nodes[id]
	return ok
}

// OnlineNodes 返回当前全部在线节点 ID。
func (r *Router) OnlineNodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	return ids
}

// RingSize 返回环上虚拟节点总数。
func (r *Router) RingSize() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tree.len()
}

// OwnerOf 返回指定虚拟节点哈希当前在环上的属主（测试与调试辅助）。
func (r *Router) OwnerOf(vnodeHashValue uint64) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tree.get(vnodeHashValue)
}
