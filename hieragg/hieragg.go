// Package hieragg 提供有向无环组织图（DAG）上的层级汇总。
//
// 关键性质：同一个叶节点可以归属多个上级；对每个上级而言，该叶只计一次；
// 不同上级各自独立计数。因此内部节点的汇总值 = 其可达叶集合（去重后）的值之和，
// 绝不能把直接子节点的汇总值相加（共享叶会被重复计算）。
//
// 为支持增量计算，每个节点维护：
//   - leafSet: 可达叶节点 ID 的去重集合（来源叶集合）
//   - sum:     该集合中叶值之和
//
// 支持的增量操作：
//   - SetLeafValue: 修改叶值，标量差量沿祖先链传播
//   - AddEdge:      新增归属边，只把“新增叶差集”向上传播
//   - RemoveEdge:   删除归属边，按拓扑序重算受影响祖先
//
// 每次操作返回 Update，包含每个受影响节点的汇总差量（Deltas）以及
// 引起变化的来源叶集合（Added / Removed）。
package hieragg

import (
	"errors"
	"fmt"
	"sort"
)

// Update 描述一次增量操作产生的结果。
type Update struct {
	// Deltas[node] 为节点汇总值的变化量（可正可负）。
	Deltas map[string]float64
	// Added[node] 为本次新进入节点来源叶集合的叶 ID（已排序）。
	Added map[string][]string
	// Removed[node] 为本次离开节点来源叶集合的叶 ID（已排序）。
	Removed map[string][]string
}

func newUpdate() Update {
	return Update{
		Deltas:  make(map[string]float64),
		Added:   make(map[string][]string),
		Removed: make(map[string][]string),
	}
}

func (u *Update) recordAdd(node string, leaves map[string]struct{}, delta float64) {
	u.Deltas[node] += delta
	u.Added[node] = unionStrings(u.Added[node], leaves)
}

func (u *Update) recordRemove(node string, leaves map[string]struct{}, delta float64) {
	u.Deltas[node] -= delta
	u.Removed[node] = unionStrings(u.Removed[node], leaves)
}

// Graph 是有向无环组织图。边方向为 上级 -> 下级（parent -> child）。
type Graph struct {
	children map[string]map[string]struct{} // parent -> children
	parents  map[string]map[string]struct{} // child -> parents
	isLeaf   map[string]bool
	leafVal  map[string]float64
	leafSet  map[string]map[string]struct{} // node -> 可达叶集合（去重）
	sum      map[string]float64             // node -> 叶值之和
}

// New 创建空图。
func New() *Graph {
	return &Graph{
		children: make(map[string]map[string]struct{}),
		parents:  make(map[string]map[string]struct{}),
		isLeaf:   make(map[string]bool),
		leafVal:  make(map[string]float64),
		leafSet:  make(map[string]map[string]struct{}),
		sum:      make(map[string]float64),
	}
}

// AddLeaf 注册叶节点及其初始值。
func (g *Graph) AddLeaf(id string, value float64) error {
	if _, ok := g.isLeaf[id]; ok {
		return fmt.Errorf("hieragg: node %q already exists", id)
	}
	g.isLeaf[id] = true
	g.leafVal[id] = value
	g.children[id] = make(map[string]struct{})
	g.parents[id] = make(map[string]struct{})
	g.leafSet[id] = map[string]struct{}{id: {}}
	g.sum[id] = value
	return nil
}

// AddNode 注册内部（汇总）节点。
func (g *Graph) AddNode(id string) error {
	if _, ok := g.isLeaf[id]; ok {
		return fmt.Errorf("hieragg: node %q already exists", id)
	}
	g.isLeaf[id] = false
	g.children[id] = make(map[string]struct{})
	g.parents[id] = make(map[string]struct{})
	g.leafSet[id] = make(map[string]struct{})
	g.sum[id] = 0
	return nil
}

// Sum 返回节点的当前汇总值（可达去重叶集合的值之和）。
func (g *Graph) Sum(id string) (float64, error) {
	if _, ok := g.isLeaf[id]; !ok {
		return 0, fmt.Errorf("hieragg: unknown node %q", id)
	}
	return g.sum[id], nil
}

// Leaves 返回节点的来源叶集合（去重、已排序）。
func (g *Graph) Leaves(id string) ([]string, error) {
	if _, ok := g.isLeaf[id]; !ok {
		return nil, fmt.Errorf("hieragg: unknown node %q", id)
	}
	return sortedKeys(g.leafSet[id]), nil
}

// SetLeafValue 修改叶值，返回所有受影响祖先的差量。
func (g *Graph) SetLeafValue(id string, value float64) (Update, error) {
	u := newUpdate()
	leaf, ok := g.isLeaf[id]
	if !ok {
		return u, fmt.Errorf("hieragg: unknown node %q", id)
	}
	if !leaf {
		return u, fmt.Errorf("hieragg: %q is not a leaf", id)
	}
	delta := value - g.leafVal[id]
	g.leafVal[id] = value
	if delta == 0 {
		return u, nil
	}
	// 叶自身及其全部祖先的 sum 同步增减；来源叶集合不变。
	for n := range g.selfAndAncestors(id) {
		g.sum[n] += delta
		u.Deltas[n] += delta
	}
	return u, nil
}

// AddEdge 添加归属边 parent -> child。
// 只把 child 的来源叶集合中 parent 尚未包含的差集向上传播，
// 因此共享叶在任何上级处都只计一次。
func (g *Graph) AddEdge(parent, child string) (Update, error) {
	u := newUpdate()
	if err := g.checkEdgeEndpoints(parent, child); err != nil {
		return u, err
	}
	if g.isLeaf[parent] {
		return u, fmt.Errorf("hieragg: leaf %q cannot have children", parent)
	}
	if _, ok := g.children[parent][child]; ok {
		return u, nil // 边已存在，幂等
	}
	if g.reachable(child, parent) {
		return u, fmt.Errorf("hieragg: edge %q -> %q would create a cycle", parent, child)
	}
	g.children[parent][child] = struct{}{}
	g.parents[child][parent] = struct{}{}

	// 工作清单：node -> 待并入的候选叶集合。只增不减，必然终止。
	pending := map[string]map[string]struct{}{
		parent: copySet(g.leafSet[child]),
	}
	for len(pending) > 0 {
		node, cand := popAny(pending)
		gained := diffSet(cand, g.leafSet[node])
		if len(gained) == 0 {
			continue
		}
		var dv float64
		for l := range gained {
			dv += g.leafVal[l]
			g.leafSet[node][l] = struct{}{}
		}
		g.sum[node] += dv
		u.recordAdd(node, gained, dv)
		for p := range g.parents[node] {
			if pending[p] == nil {
				pending[p] = make(map[string]struct{})
			}
			for l := range gained {
				pending[p][l] = struct{}{}
			}
		}
	}
	return u, nil
}

// RemoveEdge 删除归属边 parent -> child，并按拓扑序重算受影响的祖先。
func (g *Graph) RemoveEdge(parent, child string) (Update, error) {
	u := newUpdate()
	if err := g.checkEdgeEndpoints(parent, child); err != nil {
		return u, err
	}
	if _, ok := g.children[parent][child]; !ok {
		return u, fmt.Errorf("hieragg: edge %q -> %q does not exist", parent, child)
	}
	delete(g.children[parent], child)
	delete(g.parents[child], parent)

	// 受影响节点 = parent 及其全部祖先（叶不可能有出边，故都是内部节点）。
	affected := g.selfAndAncestors(parent)

	// 拓扑序（子先父后）：对受影响子图做 DFS 后序。
	order := make([]string, 0, len(affected))
	visited := make(map[string]bool, len(affected))
	var visit func(n string)
	visit = func(n string) {
		if visited[n] {
			return
		}
		visited[n] = true
		for ch := range g.children[n] {
			if affected[ch] {
				visit(ch)
			}
		}
		order = append(order, n)
	}
	for n := range affected {
		visit(n)
	}

	// 重算：newSet = 各直接子节点 leafSet 的并集。
	// 未受影响的子节点 leafSet 仍然有效，受影响子节点已先被重算。
	for _, n := range order {
		newSet := make(map[string]struct{})
		for ch := range g.children[n] {
			for l := range g.leafSet[ch] {
				newSet[l] = struct{}{}
			}
		}
		removed := diffSet(g.leafSet[n], newSet)
		added := diffSet(newSet, g.leafSet[n])
		if len(removed) == 0 && len(added) == 0 {
			continue
		}
		var rv, av float64
		for l := range removed {
			rv += g.leafVal[l]
		}
		for l := range added {
			av += g.leafVal[l]
		}
		g.leafSet[n] = newSet
		g.sum[n] += av - rv
		if len(added) > 0 {
			u.recordAdd(n, added, av)
		}
		if len(removed) > 0 {
			u.recordRemove(n, removed, rv)
		}
	}
	return u, nil
}

// checkEdgeEndpoints 校验两端节点存在。
func (g *Graph) checkEdgeEndpoints(parent, child string) error {
	if _, ok := g.isLeaf[parent]; !ok {
		return fmt.Errorf("hieragg: unknown node %q", parent)
	}
	if _, ok := g.isLeaf[child]; !ok {
		return fmt.Errorf("hieragg: unknown node %q", child)
	}
	return nil
}

// reachable 报告从 from 沿 parent->child 方向能否到达 target。
func (g *Graph) reachable(from, target string) bool {
	stack := []string{from}
	seen := map[string]bool{from: true}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == target {
			return true
		}
		for ch := range g.children[n] {
			if !seen[ch] {
				seen[ch] = true
				stack = append(stack, ch)
			}
		}
	}
	return false
}

// selfAndAncestors 返回包含 id 自身及其全部祖先的集合。
func (g *Graph) selfAndAncestors(id string) map[string]bool {
	out := map[string]bool{id: true}
	stack := []string{id}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for p := range g.parents[n] {
			if !out[p] {
				out[p] = true
				stack = append(stack, p)
			}
		}
	}
	return out
}

var errEmpty = errors.New("empty")

func popAny(m map[string]map[string]struct{}) (string, map[string]struct{}) {
	for k, v := range m {
		delete(m, k)
		return k, v
	}
	panic(errEmpty)
}

func copySet(s map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(s))
	for k := range s {
		out[k] = struct{}{}
	}
	return out
}

// diffSet 返回 a - b。
func diffSet(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func unionStrings(existing []string, s map[string]struct{}) []string {
	seen := make(map[string]bool, len(existing)+len(s))
	out := existing
	for _, v := range existing {
		seen[v] = true
	}
	for k := range s {
		if !seen[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
