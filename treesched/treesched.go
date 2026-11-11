// Package treesched 实现一棵可变依赖树上的分层加权亏额轮转（WDRR）字节调度器。
//
// 树中每个节点带有权重与父节点，节点自身可以缓存待发送字节，也可以拥有后代。
// 调度器供发送宿主在每次可写时调用 [Tree.Schedule]，按层进行加权亏额轮转：
// 兄弟节点按权重比例分享父节点获得的字节预算，节点自身的载荷作为其所在层的
// 一个虚成员，与它的孩子一同参与轮转。
//
// 树结构允许客户端反复重排。[Tree.Reparent] 保证任何重排都不会制造环：当节点
// 被挂到自己的后代之下时，先把该后代提升到节点原来的父节点；独占挂接则会把
// 目标父节点现有的其他孩子全部转到新节点之下。
package treesched

import (
	"errors"
	"sync"
)

// NodeID 是树中节点的标识。RootID 为虚拟根节点，保留不可由用户创建。
type NodeID uint32

// RootID 是虚拟根节点。根节点不承载载荷、不可阻塞、不可重排。
const RootID NodeID = 0

var (
	// ErrExists 表示新增节点的 ID 已经存在。
	ErrExists = errors.New("treesched: node id already exists")
	// ErrNotExist 表示引用了不存在的节点。
	ErrNotExist = errors.New("treesched: node does not exist")
	// ErrRootReserved 表示操作非法地作用于保留的根节点。
	ErrRootReserved = errors.New("treesched: root id is reserved")
	// ErrBadWeight 表示权重为零。权重必须大于等于 1。
	ErrBadWeight = errors.New("treesched: weight must be >= 1")
	// ErrBadBytes 表示传入了负的字节数。
	ErrBadBytes = errors.New("treesched: byte count must be >= 0")
)

// NodeInfo 是节点状态的只读快照。
type NodeInfo struct {
	ID        NodeID
	Parent    NodeID
	Weight    uint32
	Blocked   bool
	Closed    bool
	Queued    int64
	Delivered int64
	Children  []NodeID
}

// node 保存节点的全部运行时状态。
//
// 亏额计数器分两级：deficit 是该节点作为“父节点层成员”时使用的亏额；
// selfDef 是节点自身载荷作为“本层虚成员”时使用的亏额。rr 记录本层轮转
// 环上次访问到的位置，位置 0 为自身载荷虚成员（根节点没有该成员）。
type node struct {
	id       NodeID
	parent   NodeID
	weight   uint32
	children []NodeID

	queued    int64 // 自身尚未发送的字节
	delivered int64 // 自身已发送的字节

	blocked bool
	closed  bool

	subDemand int64 // 整棵子树的待发字节（含不可投递部分）
	subAvail  int64 // 整棵子树中可投递的待发字节

	deficit int64 // 作为父层成员的亏额
	selfDef int64 // 自身载荷虚成员的亏额
	rr      int   // 本层轮转环游标
}

func (n *node) selfDeliverable() bool {
	return !n.blocked && !n.closed
}

// Tree 是一棵带调度器的依赖树。零值不可用，必须通过 [New] 创建。
type Tree struct {
	mu    sync.RWMutex
	nodes map[NodeID]*node
}

// New 创建一棵只含虚拟根节点的树。
func New() *Tree {
	t := &Tree{nodes: make(map[NodeID]*node)}
	t.nodes[RootID] = &node{id: RootID, parent: RootID, weight: 1}
	return t
}

// Len 返回树中的节点总数（含虚拟根）。
func (t *Tree) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.nodes)
}

func (t *Tree) mustNodeLocked(id NodeID) (*node, error) {
	n, ok := t.nodes[id]
	if !ok {
		return nil, ErrNotExist
	}
	return n, nil
}

// isAncestorLocked 判断 ancestor 是否为 x 本身或 x 的祖先。
func (t *Tree) isAncestorLocked(ancestor, x NodeID) bool {
	cur := x
	for {
		if cur == ancestor {
			return true
		}
		if cur == RootID {
			return false
		}
		cur = t.nodes[cur].parent
	}
}

func removeChildLocked(parent *node, id NodeID) {
	for i, c := range parent.children {
		if c == id {
			parent.children = append(parent.children[:i], parent.children[i+1:]...)
			return
		}
	}
}

// Add 以给定权重把新节点挂到 parent 之下。
func (t *Tree) Add(id, parent NodeID, weight uint32) error {
	if id == RootID {
		return ErrRootReserved
	}
	if weight < 1 {
		return ErrBadWeight
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.nodes[id]; ok {
		return ErrExists
	}
	p, err := t.mustNodeLocked(parent)
	if err != nil {
		return err
	}
	n := &node{id: id, parent: parent, weight: weight}
	t.nodes[id] = n
	p.children = append(p.children, id)
	return nil
}

// SetWeight 调整节点权重，权重必须大于等于 1。
func (t *Tree) SetWeight(id NodeID, weight uint32) error {
	if weight < 1 {
		return ErrBadWeight
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNodeLocked(id)
	if err != nil {
		return err
	}
	n.weight = weight
	return nil
}

// Reparent 把 id 节点挂到 newParent 之下。
//
// 为保证无环：若 newParent 是 id 本身或 id 的后代，先把 newParent 提升到
// id 原来的父节点，再执行挂接。exclusive 为真时执行独占挂接：目标父节点
// 现有的其他孩子全部转为 id 的孩子，使 id 成为目标父节点的唯一直接孩子。
func (t *Tree) Reparent(id, newParent NodeID, exclusive bool) error {
	if id == RootID {
		return ErrRootReserved
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNodeLocked(id)
	if err != nil {
		return err
	}
	if _, err := t.mustNodeLocked(newParent); err != nil {
		return err
	}
	if id == newParent {
		// 挂到自身是空操作，否则会制造自环。
		return nil
	}

	// 防环提升：newParent 位于 id 子树内时，先把它移到 id 的原父节点。
	// 提升后 id 仍是 newParent 的后代，随后的挂接只会在该子树内上移。
	if t.isAncestorLocked(id, newParent) {
		d := t.nodes[newParent]
		dOldParent := t.nodes[d.parent]
		anchor := t.nodes[n.parent]
		removeChildLocked(dOldParent, newParent)
		anchor.children = append(anchor.children, newParent)
		d.parent = anchor.id
	}

	// 从当前父节点摘下。
	cur := t.nodes[n.parent]
	removeChildLocked(cur, id)

	// 独占挂接：目标父节点的其余孩子全部转到 id 之下。
	if exclusive {
		target := t.nodes[newParent]
		siblings := append([]NodeID(nil), target.children...)
		target.children = target.children[:0]
		for _, c := range siblings {
			child := t.nodes[c]
			child.parent = id
			n.children = append(n.children, c)
		}
	}

	n.parent = newParent
	t.nodes[newParent].children = append(t.nodes[newParent].children, id)
	t.recomputeCountersLocked()
	return nil
}

// Load 向节点自身的队列追加待发字节。
func (t *Tree) Load(id NodeID, bytes int64) error {
	if bytes < 0 {
		return ErrBadBytes
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNodeLocked(id)
	if err != nil {
		return err
	}
	if bytes == 0 {
		return nil
	}
	n.queued += bytes
	deliverable := n.selfDeliverable()
	cur := n
	for {
		cur.subDemand += bytes
		if deliverable {
			cur.subAvail += bytes
		}
		if cur.id == RootID {
			break
		}
		cur = t.nodes[cur.parent]
	}
	return nil
}

// SetBlocked 标记节点阻塞或解除阻塞。
//
// 阻塞只冻结节点自身的载荷：阻塞节点仍参与父层轮转并持有亏额，其活跃
// 后代照常获得字节份额。阻塞期间累计的亏额在解除阻塞后继续生效。
func (t *Tree) SetBlocked(id NodeID, blocked bool) error {
	if id == RootID {
		return ErrRootReserved
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNodeLocked(id)
	if err != nil {
		return err
	}
	if n.closed {
		return nil
	}
	if n.blocked == blocked || n.closed {
		n.blocked = blocked
		return nil
	}
	n.blocked = blocked
	if blocked {
		t.adjustAvailLocked(n, -n.queued)
	} else {
		t.adjustAvailLocked(n, n.queued)
	}
	return nil
}

// Close 永久关闭节点。节点自身剩余载荷被冻结且不再投递，已投递字节保留；
// 后代节点不受影响，仍可正常获得份额。关闭不可逆转。
func (t *Tree) Close(id NodeID) error {
	if id == RootID {
		return ErrRootReserved
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.mustNodeLocked(id)
	if err != nil {
		return err
	}
	if !n.closed && n.selfDeliverable() {
		t.adjustAvailLocked(n, -n.queued)
	}
	n.closed = true
	n.blocked = true
	return nil
}

// adjustAvailLocked 沿祖先链调整可投递计数（不影响待发总量）。
func (t *Tree) adjustAvailLocked(n *node, delta int64) {
	cur := n
	for {
		cur.subAvail += delta
		if cur.id == RootID {
			return
		}
		cur = t.nodes[cur.parent]
	}
}

// recomputeCountersLocked 以后序遍历重算所有子树计数，供结构性重排后使用。
func (t *Tree) recomputeCountersLocked() {
	order := make([]NodeID, 0, len(t.nodes))
	stack := []NodeID{RootID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, id)
		stack = append(stack, t.nodes[id].children...)
	}
	for i := len(order) - 1; i >= 0; i-- {
		n := t.nodes[order[i]]
		var demand, avail int64
		if n.id != RootID {
			demand = n.queued
			if n.selfDeliverable() {
				avail = n.queued
			}
		}
		for _, c := range n.children {
			child := t.nodes[c]
			demand += child.subDemand
			avail += child.subAvail
		}
		n.subDemand = demand
		n.subAvail = avail
	}
}

// Schedule 进行一轮调度，最多发送 maxBytes 字节，返回实际发送的字节数。
//
// 每层按加权亏额轮转选择成员：成员每次被轮到时先按权重补充亏额，再在
// 亏额、子树需求与剩余预算的最小值内发送。没有可投递载荷时返回 0。
func (t *Tree) Schedule(maxBytes int64) int64 {
	if maxBytes <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.serveLevel(t.nodes[RootID], maxBytes)
}

// ringSize 返回某一层轮转环的成员数：非根节点含自身载荷虚成员。
func ringSize(n *node) int {
	if n.id == RootID {
		return len(n.children)
	}
	return len(n.children) + 1
}

// ringMember 把环位置映射为成员。isSelf 为真时表示节点自身载荷虚成员。
func ringMember(n *node, pos int) (id NodeID, isSelf bool) {
	if n.id == RootID {
		return n.children[pos], false
	}
	if pos == 0 {
		return n.id, true
	}
	return n.children[pos-1], false
}

// serveLevel 在节点 n 所在层执行 WDRR，并递归把预算下发给子树。
//
// 阻塞节点的自身虚成员不可投递，但其孩子仍在同一环中参与轮转，因此活跃
// 后代可以照常获得份额。
func (t *Tree) serveLevel(n *node, budget int64) int64 {
	if budget <= 0 || n.subAvail <= 0 {
		return 0
	}
	size := ringSize(n)
	if size == 0 {
		return 0
	}
	if n.rr >= size {
		n.rr = 0
	}
	served := int64(0)
	progress := true
	for budget > 0 && progress {
		progress = false
		start := n.rr
		for step := 0; step < size && budget > 0; step++ {
			pos := (start + step) % size
			n.rr = (pos + 1) % size

			id, isSelf := ringMember(n, pos)
			if isSelf {
				if !n.selfDeliverable() || n.queued == 0 {
					n.selfDef = 0
					continue
				}
				n.selfDef += int64(n.weight)
				take := min64(budget, min64(n.selfDef, n.queued))
				got := t.serveSelf(n, take)
				n.selfDef -= got
				budget -= got
				served += got
				if got > 0 {
					progress = true
				}
				continue
			}

			child := t.nodes[id]
			if child.subAvail <= 0 {
				// 不活跃成员的亏额必须清零，避免恢复后突发占用带宽。
				child.deficit = 0
				continue
			}
			child.deficit += int64(child.weight)
			take := min64(budget, min64(child.deficit, child.subAvail))
			got := t.serveSubtree(child, take)
			child.deficit -= got
			budget -= got
			served += got
			if got > 0 {
				progress = true
			}
		}
	}
	return served
}

// serveSubtree 把预算交给 child 子树：先在 child 自身层分发，其内部各层
// 递归执行同样的 WDRR。
func (t *Tree) serveSubtree(child *node, budget int64) int64 {
	if budget <= 0 {
		return 0
	}
	return t.serveLevel(child, budget)
}

// serveSelf 从节点自身队列投递字节，并同步维护祖先链上的两个子树计数。
func (t *Tree) serveSelf(n *node, bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	if bytes > n.queued {
		bytes = n.queued
	}
	n.queued -= bytes
	n.delivered += bytes
	cur := n
	for {
		cur.subDemand -= bytes
		cur.subAvail -= bytes
		if cur.id == RootID {
			break
		}
		cur = t.nodes[cur.parent]
	}
	return bytes
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// Snapshot 返回整棵树的只读快照，键为节点 ID。
func (t *Tree) Snapshot() map[NodeID]NodeInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[NodeID]NodeInfo, len(t.nodes))
	for id, n := range t.nodes {
		children := append([]NodeID(nil), n.children...)
		out[id] = NodeInfo{
			ID:        n.id,
			Parent:    n.parent,
			Weight:    n.weight,
			Blocked:   n.blocked,
			Closed:    n.closed,
			Queued:    n.queued,
			Delivered: n.delivered,
			Children:  children,
		}
	}
	return out
}

func (t *Tree) nodeSnapshot(id NodeID) (NodeInfo, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n, err := t.mustNodeLocked(id)
	if err != nil {
		return NodeInfo{}, err
	}
	return NodeInfo{
		ID:        n.id,
		Parent:    n.parent,
		Weight:    n.weight,
		Blocked:   n.blocked,
		Closed:    n.closed,
		Queued:    n.queued,
		Delivered: n.delivered,
		Children:  append([]NodeID(nil), n.children...),
	}, nil
}

// Node 返回单个节点的只读快照。
func (t *Tree) Node(id NodeID) (NodeInfo, error) {
	return t.nodeSnapshot(id)
}

// Queued 返回节点自身尚未投递的字节数。
func (t *Tree) Queued(id NodeID) (int64, error) {
	info, err := t.nodeSnapshot(id)
	if err != nil {
		return 0, err
	}
	return info.Queued, nil
}

// Delivered 返回节点自身累计已投递的字节数。
func (t *Tree) Delivered(id NodeID) (int64, error) {
	info, err := t.nodeSnapshot(id)
	if err != nil {
		return 0, err
	}
	return info.Delivered, nil
}

// Totals 遍历全树返回待发与已投递的字节总量。
func (t *Tree) Totals() (queued, delivered int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, n := range t.nodes {
		queued += n.queued
		delivered += n.delivered
	}
	return queued, delivered
}
