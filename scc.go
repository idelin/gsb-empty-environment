// Package scc 提供动态有向图强连通分量（SCC）的增量维护。
//
// 支持顶点与有向边的动态增删。边插入时通过局部可达性遍历（增量缩点）
// 判断是否有分量合并；边删除时仅对受影响的旧分量做局部 Tarjan 重算，
// 判断其是否分裂。每次变更返回描述合并/分裂的 Delta。
// 每个分量以其成员中的最小节点 ID 作为稳定标识。
package scc

import "sort"

// Component 表示一个强连通分量。
type Component struct {
	ID    string   // 分量标识：成员中的最小节点 ID
	Nodes []string // 成员节点，升序
}

// MergeEvent 描述一次分量合并：若干旧分量合并为一个新分量。
type MergeEvent struct {
	New    Component // 合并后的新分量
	OldIDs []string  // 被合并的旧分量 ID，升序
}

// SplitEvent 描述一次分量分裂：一个旧分量分裂为若干新分量。
type SplitEvent struct {
	OldID string      // 分裂前的旧分量 ID
	Parts []Component // 分裂出的新分量，按 ID 升序
}

// Delta 描述一次图变更引起的 SCC 差量。
type Delta struct {
	Merges []MergeEvent
	Splits []SplitEvent
}

// Empty 报告差量是否为空（无合并也无分裂）。
func (d Delta) Empty() bool { return len(d.Merges) == 0 && len(d.Splits) == 0 }

// Tracker 增量维护动态有向图的强连通分量。
type Tracker struct {
	out     map[string]map[string]struct{} // 正向邻接表
	in      map[string]map[string]struct{} // 反向邻接表
	comp    map[string]string              // 节点 -> 分量 ID
	members map[string]map[string]struct{} // 分量 ID -> 节点集合
}

// NewTracker 创建一个空的 SCC 维护器。
func NewTracker() *Tracker {
	return &Tracker{
		out:     make(map[string]map[string]struct{}),
		in:      make(map[string]map[string]struct{}),
		comp:    make(map[string]string),
		members: make(map[string]map[string]struct{}),
	}
}

// AddVertex 添加顶点（幂等）。新顶点自成一个单节点分量。
func (t *Tracker) AddVertex(v string) {
	if _, ok := t.out[v]; ok {
		return
	}
	t.out[v] = make(map[string]struct{})
	t.in[v] = make(map[string]struct{})
	t.comp[v] = v
	t.members[v] = map[string]struct{}{v: {}}
}

// RemoveVertex 删除顶点及其所有关联边。
// 若其所在分量因此分裂，返回相应的分裂差量。
func (t *Tracker) RemoveVertex(v string) Delta {
	if _, ok := t.out[v]; !ok {
		return Delta{}
	}
	for w := range t.out[v] {
		delete(t.in[w], v)
	}
	for w := range t.in[v] {
		delete(t.out[w], v)
	}
	oldID := t.comp[v]
	delete(t.out, v)
	delete(t.in, v)
	delete(t.comp, v)
	rest := t.members[oldID]
	delete(rest, v)
	delete(t.members, oldID)
	if len(rest) == 0 {
		return Delta{}
	}
	return t.recompute(rest, oldID)
}

// AddEdge 插入有向边 from->to（端点不存在时自动补建，幂等）。
// 若该边使原本不可互达的分量形成环，则沿 v->...->u 路径上的
// 所有分量合并为一个新分量，并返回合并差量。
func (t *Tracker) AddEdge(u, v string) Delta {
	t.AddVertex(u)
	t.AddVertex(v)
	if _, ok := t.out[u][v]; ok {
		return Delta{}
	}
	t.out[u][v] = struct{}{}
	t.in[v][u] = struct{}{}

	if t.comp[u] == t.comp[v] {
		return Delta{} // 分量内部的边不改变 SCC
	}
	// 局部前向遍历：v 能否到达 u？不能则缩点 DAG 上只是多了一条边。
	fwd := reachable(t.out, v)
	if _, ok := fwd[u]; !ok {
		return Delta{}
	}
	// 局部反向遍历：能到达 u 的节点。两者交集即新环上的全部节点。
	bwd := reachable(t.in, u)
	oldIDs := make(map[string]struct{})
	for n := range fwd {
		if _, ok := bwd[n]; ok {
			oldIDs[t.comp[n]] = struct{}{}
		}
	}
	// 增量缩点：合并涉及的所有旧分量。
	merged := make(map[string]struct{})
	for id := range oldIDs {
		for n := range t.members[id] {
			merged[n] = struct{}{}
		}
		delete(t.members, id)
	}
	newID := minKey(merged)
	t.members[newID] = merged
	for n := range merged {
		t.comp[n] = newID
	}
	return Delta{Merges: []MergeEvent{{
		New:    Component{ID: newID, Nodes: sortedKeys(merged)},
		OldIDs: sortedKeys(oldIDs),
	}}}
}

// RemoveEdge 删除有向边 from->to（不存在时为空操作）。
// 若该边所在分量因此不再强连通，则对该分量做局部重算并返回分裂差量。
func (t *Tracker) RemoveEdge(u, v string) Delta {
	if _, ok := t.out[u][v]; !ok {
		return Delta{}
	}
	delete(t.out[u], v)
	delete(t.in[v], u)

	if t.comp[u] != t.comp[v] {
		return Delta{} // 跨分量边的删除不影响任何 SCC
	}
	oldID := t.comp[u]
	nodes := t.members[oldID]
	delete(t.members, oldID)
	return t.recompute(nodes, oldID)
}

// ComponentOf 返回节点当前所属分量的 ID。
func (t *Tracker) ComponentOf(v string) (string, bool) {
	id, ok := t.comp[v]
	return id, ok
}

// Components 返回当前全部分量，按 ID 升序。
func (t *Tracker) Components() []Component {
	ids := make([]string, 0, len(t.members))
	for id := range t.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cs := make([]Component, 0, len(ids))
	for _, id := range ids {
		cs = append(cs, Component{ID: id, Nodes: sortedKeys(t.members[id])})
	}
	return cs
}

// recompute 对单个旧分量诱导的子图做局部 Tarjan 重算，
// 更新分量映射；若分裂为多个分量则返回分裂差量。
func (t *Tracker) recompute(nodes map[string]struct{}, oldID string) Delta {
	sccs := tarjan(t.out, nodes)
	if len(sccs) == 1 {
		id := minKey(sccs[0])
		t.members[id] = sccs[0]
		for n := range sccs[0] {
			t.comp[n] = id
		}
		return Delta{}
	}
	ev := SplitEvent{OldID: oldID}
	for _, s := range sccs {
		id := minKey(s)
		t.members[id] = s
		for n := range s {
			t.comp[n] = id
		}
		ev.Parts = append(ev.Parts, Component{ID: id, Nodes: sortedKeys(s)})
	}
	sort.Slice(ev.Parts, func(i, j int) bool { return ev.Parts[i].ID < ev.Parts[j].ID })
	return Delta{Splits: []SplitEvent{ev}}
}

// reachable 从 start 出发在邻接表 adj 上做局部遍历，返回可达节点集合。
func reachable(adj map[string]map[string]struct{}, start string) map[string]struct{} {
	seen := map[string]struct{}{start: {}}
	stack := []string{start}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for w := range adj[v] {
			if _, ok := seen[w]; !ok {
				seen[w] = struct{}{}
				stack = append(stack, w)
			}
		}
	}
	return seen
}

// tarjan 在 nodes 诱导的子图上运行 Tarjan 算法，返回全部 SCC（节点集合）。
func tarjan(out map[string]map[string]struct{}, nodes map[string]struct{}) []map[string]struct{} {
	index := make(map[string]int, len(nodes))
	low := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	var stack []string
	counter := 0
	var result []map[string]struct{}

	var strongconnect func(v string)
	strongconnect = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for w := range out[v] {
			if _, ok := nodes[w]; !ok {
				continue // 只考虑诱导子图内的边
			}
			if _, seen := index[w]; !seen {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			s := make(map[string]struct{})
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				s[w] = struct{}{}
				if w == v {
					break
				}
			}
			result = append(result, s)
		}
	}
	for v := range nodes {
		if _, seen := index[v]; !seen {
			strongconnect(v)
		}
	}
	return result
}

func minKey(set map[string]struct{}) string {
	min := ""
	for k := range set {
		if min == "" || k < min {
			min = k
		}
	}
	return min
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
