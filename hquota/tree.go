// Package hquota 实现层级配额库：按树结构分配整数额度。
//
// 内部节点持有总上限（Cap），叶节点持有需求（Demand）与权重（Weight）。
// 分配算法为加权最大最小公平（weighted max-min fairness）的渐进注水法，
// 先在实数域求出精确公平份额，再一次性向下取整，剩余零头按叶编号轮转
// 分配，游标跨批次保留。绝不会在每一层先取整而丢掉可用额度。
package hquota

import (
	"fmt"
	"sort"
)

// Node 是树的内部节点（含根），持有子树总额度上限。
type Node struct {
	ID       string
	Cap      int64
	Parent   *Node
	Children []*Node
	Leaves   []*Leaf
}

// Leaf 是叶节点，持有需求与权重。ID 为叶编号，决定零头轮转顺序。
type Leaf struct {
	ID     int
	Demand int64
	Weight int64
	Parent *Node
}

// Tree 是配额树。
type Tree struct {
	Root   *Node
	nodes  map[string]*Node
	leaves map[int]*Leaf
	order  []*Leaf // 按叶编号升序
}

// NewTree 创建只有根节点的配额树。
func NewTree(rootID string, rootCap int64) (*Tree, error) {
	if rootCap < 0 {
		return nil, fmt.Errorf("hquota: root cap must be >= 0, got %d", rootCap)
	}
	root := &Node{ID: rootID, Cap: rootCap}
	return &Tree{
		Root:   root,
		nodes:  map[string]*Node{rootID: root},
		leaves: make(map[int]*Leaf),
	}, nil
}

// AddNode 在 parentID 下新增内部节点。
func (t *Tree) AddNode(parentID, id string, cap int64) error {
	if cap < 0 {
		return fmt.Errorf("hquota: node %q cap must be >= 0, got %d", id, cap)
	}
	if _, dup := t.nodes[id]; dup {
		return fmt.Errorf("hquota: node %q already exists", id)
	}
	p, ok := t.nodes[parentID]
	if !ok {
		return fmt.Errorf("hquota: parent node %q not found", parentID)
	}
	n := &Node{ID: id, Cap: cap, Parent: p}
	p.Children = append(p.Children, n)
	t.nodes[id] = n
	return nil
}

// AddLeaf 在 parentID 下新增叶节点。weight 必须 >= 1，demand 必须 >= 0。
func (t *Tree) AddLeaf(parentID string, leafID int, demand, weight int64) error {
	if demand < 0 {
		return fmt.Errorf("hquota: leaf %d demand must be >= 0, got %d", leafID, demand)
	}
	if weight < 1 {
		return fmt.Errorf("hquota: leaf %d weight must be >= 1, got %d", leafID, weight)
	}
	if _, dup := t.leaves[leafID]; dup {
		return fmt.Errorf("hquota: leaf %d already exists", leafID)
	}
	p, ok := t.nodes[parentID]
	if !ok {
		return fmt.Errorf("hquota: parent node %q not found", parentID)
	}
	lf := &Leaf{ID: leafID, Demand: demand, Weight: weight, Parent: p}
	p.Leaves = append(p.Leaves, lf)
	t.leaves[leafID] = lf
	t.order = append(t.order, lf)
	sort.Slice(t.order, func(i, j int) bool { return t.order[i].ID < t.order[j].ID })
	return nil
}

// SetCap 调整节点额度上限（例如额度缩小），随后需重新调用 Allocate。
func (t *Tree) SetCap(nodeID string, cap int64) error {
	if cap < 0 {
		return fmt.Errorf("hquota: node %q cap must be >= 0, got %d", nodeID, cap)
	}
	n, ok := t.nodes[nodeID]
	if !ok {
		return fmt.Errorf("hquota: node %q not found", nodeID)
	}
	n.Cap = cap
	return nil
}

// Leaves 返回按叶编号升序的全部叶节点。
func (t *Tree) Leaves() []*Leaf {
	out := make([]*Leaf, len(t.order))
	copy(out, t.order)
	return out
}

// leavesUnder 返回子树内全部叶节点（按叶编号升序）。
func (t *Tree) leavesUnder(n *Node) []*Leaf {
	var out []*Leaf
	var walk func(cur *Node)
	walk = func(cur *Node) {
		out = append(out, cur.Leaves...)
		for _, c := range cur.Children {
			walk(c)
		}
	}
	walk(n)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// subtreeSum 统计子树内整数额度合计。
func (t *Tree) subtreeSum(n *Node, alloc map[int]int64) int64 {
	var sum int64
	for _, lf := range t.leavesUnder(n) {
		sum += alloc[lf.ID]
	}
	return sum
}
