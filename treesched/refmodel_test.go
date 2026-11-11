package treesched

// refNode 是独立于被测实现的参考树节点：只保留结构、状态与计费账面，
// 不包含任何调度逻辑，用于交叉验证重排无环与字节守恒。
type refNode struct {
	parent    uint32
	weight    uint32
	blocked   bool
	closed    bool
	children  map[uint32]struct{}
	loaded    int64
	delivered int64
}

type refTree struct {
	nodes map[uint32]*refNode
}

func newRefTree() *refTree {
	r := &refTree{nodes: map[uint32]*refNode{}}
	r.nodes[uint32(RootID)] = &refNode{
		parent:   uint32(RootID),
		weight:   1,
		children: map[uint32]struct{}{},
	}
	return r
}

func (r *refTree) add(id, parent uint32, weight uint32) {
	r.nodes[id] = &refNode{
		parent:   parent,
		weight:   weight,
		children: map[uint32]struct{}{},
	}
	r.nodes[parent].children[id] = struct{}{}
}

func (r *refTree) setWeight(id uint32, weight uint32) {
	r.nodes[id].weight = weight
}

func (r *refTree) load(id uint32, bytes int64) {
	r.nodes[id].loaded += bytes
}

func (r *refTree) setBlocked(id uint32, blocked bool) {
	if r.nodes[id].closed {
		return
	}
	r.nodes[id].blocked = blocked
}

func (r *refTree) close(id uint32) {
	r.nodes[id].closed = true
	r.nodes[id].blocked = true
}

// reparent 用与产品实现相同的语义独立模拟重排，便于断言两边结构一致。
func (r *refTree) reparent(id, newParent uint32, exclusive bool) {
	if id == newParent {
		return
	}
	if r.isAncestor(id, newParent) {
		d := r.nodes[newParent]
		oldParentID := d.parent
		anchorID := r.nodes[id].parent
		delete(r.nodes[oldParentID].children, newParent)
		r.nodes[anchorID].children[newParent] = struct{}{}
		d.parent = anchorID
	}
	n := r.nodes[id]
	oldParent := r.nodes[n.parent]
	delete(oldParent.children, id)

	if exclusive {
		target := r.nodes[newParent]
		for c := range target.children {
			r.nodes[c].parent = id
			n.children[c] = struct{}{}
		}
		target.children = map[uint32]struct{}{}
	}
	n.parent = newParent
	r.nodes[newParent].children[id] = struct{}{}
}

func (r *refTree) isAncestor(ancestor, x uint32) bool {
	cur := x
	for {
		if cur == ancestor {
			return true
		}
		if cur == uint32(RootID) {
			return false
		}
		cur = r.nodes[cur].parent
	}
}

// assertAcyclic 验证参考树是一棵以根为唯一汇聚点的树：每个节点沿父指针
// 上溯都必须在有限步内到达根，且不出现自环或环。
func (r *refTree) assertAcyclic() bool {
	childCount := map[uint32]int{}
	for id, n := range r.nodes {
		if id == uint32(RootID) {
			if n.parent != uint32(RootID) {
				return false
			}
		} else if n.parent == id {
			return false
		}
		if _, ok := r.nodes[n.parent]; !ok {
			return false
		}
		childCount[n.parent]++
	}
	for id := range r.nodes {
		seen := map[uint32]struct{}{}
		cur := id
		for {
			if cur == uint32(RootID) {
				break
			}
			if _, dup := seen[cur]; dup {
				return false
			}
			seen[cur] = struct{}{}
			cur = r.nodes[cur].parent
		}
	}
	// 孩子集合与父指针必须双向一致。
	actualChildren := map[uint32]int{}
	for id, n := range r.nodes {
		if id != uint32(RootID) {
			actualChildren[n.parent]++
		}
	}
	for id, n := range r.nodes {
		if len(n.children) != childCount[id] {
			return false
		}
	}
	return true
}

// conservation 统计参考账面：所有节点的已投递 + 未投递必须等于累计载入。
// 未投递只统计未关闭节点（关闭节点的残留载荷按语义永久冻结，单独核对）。
func (r *refTree) conservation() (loaded, delivered, frozen int64) {
	for _, n := range r.nodes {
		loaded += n.loaded
		delivered += n.delivered
		if n.closed {
			frozen += n.loaded - n.delivered
		}
	}
	return loaded, delivered, frozen
}

// syncDelivered 用被测树的实际投递结果回填参考账面，随后两侧即可做守恒比对。
func (r *refTree) syncDelivered(snap map[NodeID]NodeInfo) {
	for id, info := range snap {
		if n, ok := r.nodes[uint32(id)]; ok {
			n.delivered = info.Delivered
		}
	}
}
