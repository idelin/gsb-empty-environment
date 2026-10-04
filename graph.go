// Package dagagg 维护有向无环组织图上的层级汇总。
//
// 图中每个节点都独立维护“可到达叶节点集合”（去重），因此：
//   - 同一个上级对同一个叶只计一次（即使存在多条路径）；
//   - 不同上级各自独立汇总，共享叶会被分别计入；
//   - 绝不把子节点的汇总值直接相加（那会重复计算共享叶）。
//
// 支持叶值修改与归属边增删，所有变更操作都返回差量（Delta），
// 其中携带引发该差量的来源叶集合。
package dagagg

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// NodeID 是节点的唯一标识。
type NodeID string

var (
	ErrNodeExists   = errors.New("dagagg: node already exists")
	ErrNodeNotFound = errors.New("dagagg: node not found")
	ErrNotLeaf      = errors.New("dagagg: node is not a leaf")
	ErrEdgeExists   = errors.New("dagagg: edge already exists")
	ErrEdgeNotFound = errors.New("dagagg: edge not found")
	ErrCycle        = errors.New("dagagg: edge would create a cycle")
)

// Delta 描述一次变更对某个节点汇总值的影响。
type Delta struct {
	Node    NodeID   // 受影响的节点
	Value   float64  // 汇总值的有符号变化量
	Added   []NodeID // 新纳入该节点汇总的来源叶
	Removed []NodeID // 从该节点汇总中移除的来源叶
	Changed []NodeID // 自身取值发生变化的来源叶（仅 SetLeafValue）
}

type node struct {
	leaf     bool
	value    float64 // 仅叶节点有效：自身取值
	agg      float64 // 汇总值 = 可到达叶集合中各叶 value 之和
	parents  map[NodeID]struct{}
	children map[NodeID]struct{}
	leaves   map[NodeID]struct{} // 可到达叶集合（去重）；叶节点包含自身
}

// Graph 是一张有向无环组织图。所有方法并发安全。
type Graph struct {
	mu    sync.RWMutex
	nodes map[NodeID]*node
}

// NewGraph 创建空图。
func NewGraph() *Graph {
	return &Graph{nodes: make(map[NodeID]*node)}
}

// AddLeaf 添加取值为 value 的叶节点。
func (g *Graph) AddLeaf(id NodeID, value float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; ok {
		return fmt.Errorf("%w: %s", ErrNodeExists, id)
	}
	g.nodes[id] = &node{
		leaf:     true,
		value:    value,
		agg:      value,
		parents:  make(map[NodeID]struct{}),
		children: make(map[NodeID]struct{}),
		leaves:   map[NodeID]struct{}{id: {}},
	}
	return nil
}

// AddNode 添加内部（汇总）节点。
func (g *Graph) AddNode(id NodeID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[id]; ok {
		return fmt.Errorf("%w: %s", ErrNodeExists, id)
	}
	g.nodes[id] = &node{
		parents:  make(map[NodeID]struct{}),
		children: make(map[NodeID]struct{}),
		leaves:   make(map[NodeID]struct{}),
	}
	return nil
}

// AddEdge 添加归属边 parent -> child（child 可以是内部节点或叶）。
// 若会形成环则返回 ErrCycle。返回所有受影响节点的差量。
func (g *Graph) AddEdge(parent, child NodeID) ([]Delta, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.nodes[parent]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, parent)
	}
	c, ok := g.nodes[child]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, child)
	}
	if _, ok := p.children[child]; ok {
		return nil, fmt.Errorf("%w: %s -> %s", ErrEdgeExists, parent, child)
	}
	// 若 child 沿孩子方向可达 parent，则新边成环。
	if parent == child || g.reaches(child, parent) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrCycle, parent, child)
	}

	p.children[child] = struct{}{}
	c.parents[parent] = struct{}{}

	// 只把 parent 尚未包含的叶向上传播；已包含的叶说明存在其他路径，
	// 按“同一上级只计一次”的要求无需处理。
	var deltas []Delta
	g.addLeaves(parent, diffKeys(c.leaves, p.leaves), &deltas)
	return deltas, nil
}

// RemoveEdge 移除归属边 parent -> child，返回所有受影响节点的差量。
func (g *Graph) RemoveEdge(parent, child NodeID) ([]Delta, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.nodes[parent]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, parent)
	}
	if _, ok := g.nodes[child]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, child)
	}
	if _, ok := p.children[child]; !ok {
		return nil, fmt.Errorf("%w: %s -> %s", ErrEdgeNotFound, parent, child)
	}

	delete(p.children, child)
	delete(g.nodes[child].parents, parent)

	// parent 丢失的叶 = 原可到达、且其余孩子都不再可达的叶。
	lost := make(map[NodeID]struct{})
	for l := range p.leaves {
		if !g.reachableViaChildren(p, l) {
			lost[l] = struct{}{}
		}
	}
	var deltas []Delta
	g.removeLeaves(parent, lost, &deltas)
	return deltas, nil
}

// SetLeafValue 修改叶节点取值，返回该叶及其所有祖先节点的差量。
func (g *Graph) SetLeafValue(id NodeID, value float64) ([]Delta, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok := g.nodes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	if !n.leaf {
		return nil, fmt.Errorf("%w: %s", ErrNotLeaf, id)
	}
	d := value - n.value
	n.value = value
	n.agg = value

	deltas := []Delta{{Node: id, Value: d, Changed: []NodeID{id}}}
	// 沿父边向上遍历全部祖先（去重），每个祖先的汇总值都变化 d。
	visited := map[NodeID]struct{}{id: {}}
	queue := []NodeID{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for p := range g.nodes[cur].parents {
			if _, ok := visited[p]; ok {
				continue
			}
			visited[p] = struct{}{}
			g.nodes[p].agg += d
			deltas = append(deltas, Delta{Node: p, Value: d, Changed: []NodeID{id}})
			queue = append(queue, p)
		}
	}
	return deltas, nil
}

// Aggregate 返回节点的当前汇总值。
func (g *Graph) Aggregate(id NodeID) (float64, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	return n.agg, nil
}

// SourceLeaves 返回节点当前汇总所依据的来源叶集合（去重、排序）。
func (g *Graph) SourceLeaves(id NodeID) ([]NodeID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	return sortedKeys(n.leaves), nil
}

// addLeaves 把新增来源叶集合并入 id，并沿父边递归向上传播。
func (g *Graph) addLeaves(id NodeID, added map[NodeID]struct{}, out *[]Delta) {
	if len(added) == 0 {
		return
	}
	n := g.nodes[id]
	var sum float64
	for l := range added {
		n.leaves[l] = struct{}{}
		sum += g.nodes[l].value
	}
	n.agg += sum
	*out = append(*out, Delta{Node: id, Value: sum, Added: sortedKeys(added)})
	for p := range n.parents {
		g.addLeaves(p, diffKeys(added, g.nodes[p].leaves), out)
	}
}

// removeLeaves 把失效来源叶集合从 id 中剔除，并沿父边递归向上传播。
func (g *Graph) removeLeaves(id NodeID, lost map[NodeID]struct{}, out *[]Delta) {
	if len(lost) == 0 {
		return
	}
	n := g.nodes[id]
	var sum float64
	for l := range lost {
		delete(n.leaves, l)
		sum += g.nodes[l].value
	}
	n.agg -= sum
	*out = append(*out, Delta{Node: id, Value: -sum, Removed: sortedKeys(lost)})
	for p := range n.parents {
		pn := g.nodes[p]
		up := make(map[NodeID]struct{})
		for l := range lost {
			// 上级只有在没有任何其他孩子能到达该叶时才失去它。
			if !g.reachableViaChildren(pn, l) {
				up[l] = struct{}{}
			}
		}
		g.removeLeaves(p, up, out)
	}
}

// reaches 判断沿孩子方向 from 是否可达 to（用于成环检测）。
func (g *Graph) reaches(from, to NodeID) bool {
	visited := map[NodeID]struct{}{from: {}}
	queue := []NodeID{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for c := range g.nodes[cur].children {
			if c == to {
				return true
			}
			if _, ok := visited[c]; !ok {
				visited[c] = struct{}{}
				queue = append(queue, c)
			}
		}
	}
	return false
}

// reachableViaChildren 判断 n 的某个孩子当前的来源叶集合中是否包含 l。
func (g *Graph) reachableViaChildren(n *node, l NodeID) bool {
	for c := range n.children {
		if _, ok := g.nodes[c].leaves[l]; ok {
			return true
		}
	}
	return false
}

func diffKeys(a, b map[NodeID]struct{}) map[NodeID]struct{} {
	out := make(map[NodeID]struct{}, len(a))
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func sortedKeys(m map[NodeID]struct{}) []NodeID {
	out := make([]NodeID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
