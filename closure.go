// Package netsec 实现隔离区（quarantine）对象闭包核验。
//
// 隔离目录内的一个路径并不保证该路径指向的文件对象只被隔离区引用：
// 硬链接可以让同一个文件对象同时拥有隔离区内外多个名字。本包把输入的
// 目录树抽象为“节点（目录项）—对象（(对象号, 代次) 标识的 inode）”图，
// 分别从隔离区根和宿主侧锚点出发做最短路径搜索，报告未列入允许集合的
// 跨域共享对象及其两侧最短路径。
package netsec

import (
	"errors"
	"sort"
)

// ObjectID 以对象号加代次唯一标识一个文件对象。
// 仅用对象号不足以区分对象：对象号在对象释放后可被新对象复用，
// 代次（generation）用于把“同号不同代”的对象区分开。
type ObjectID struct {
	Number     uint64
	Generation uint64
}

// Entry 是目录中的一个目录项。同一对象可以通过多个节点、多个 Entry 出现，
// 这正是硬链接在目录树中的表示。
type Entry struct {
	Name    string
	ChildID string
}

// Node 表示目录树中的一个节点（目录或文件），引用一个文件对象。
// 多个 Node 可以引用同一个 ObjectID，用于建模同一对象的多个硬链接名字；
// 目录的 Entries 允许形成环（通过后代目录项回指祖先节点）。
type Node struct {
	ID      string
	Object  ObjectID
	IsDir   bool
	Entries []Entry
}

// OutsideRef 描述宿主侧（隔离区外）的一个锚点目录项，
// 即从隔离区之外指向树中某个节点的硬链接入口。
type OutsideRef struct {
	Name    string
	ChildID string
}

// Tree 是闭包核验的输入：隔离区根、节点表以及宿主侧锚点集合。
type Tree struct {
	RootID      string
	Nodes       map[string]*Node
	OutsideRefs []OutsideRef
}

// Report 描述一个未经允许的跨域共享对象。
// InsidePath 是从隔离区根到该对象任一节点的最短路径（相对路径段）；
// OutsidePath 是从宿主侧锚点到该对象任一节点的最短路径，
// 第一段为锚点名，其后为路径段。
type Report struct {
	Object      ObjectID
	InsidePath  []string
	OutsidePath []string
}

// objState 保存单个对象当前已知的两侧最短路径。
type objState struct {
	inside  []string
	outside []string
}

// Checker 持有一次核验的完整状态。构造完成后可重复调用 Reports，
// 并可通过 Unlink 增量断开硬链接；Checker 不是并发安全的。
type Checker struct {
	tree    *Tree
	allowed map[ObjectID]struct{}
	states  map[ObjectID]*objState
}

var (
	// ErrEmptyTree 表示输入树缺少根或节点表。
	ErrEmptyTree = errors.New("netsec: empty tree")
	// ErrUnknownNode 表示引用了节点表中不存在的节点。
	ErrUnknownNode = errors.New("netsec: unknown node referenced")
	// ErrNotDirectory 表示对非目录节点执行目录项操作。
	ErrNotDirectory = errors.New("netsec: node is not a directory")
	// ErrEntryNotFound 表示待断开的目录项不存在。
	ErrEntryNotFound = errors.New("netsec: entry not found")
)

// NewChecker 校验输入树并完成初始闭包核验。
// allowed 为允许跨域共享的对象集合，其中的对象不会出现在报告中。
func NewChecker(tree *Tree, allowed map[ObjectID]struct{}) (*Checker, error) {
	if tree == nil || len(tree.Nodes) == 0 || tree.RootID == "" {
		return nil, ErrEmptyTree
	}
	root, ok := tree.Nodes[tree.RootID]
	if !ok {
		return nil, ErrUnknownNode
	}

	seen := make(map[string]bool, len(tree.Nodes))
	var validate func(node *Node) error
	validate = func(node *Node) error {
		if seen[node.ID] {
			return nil // 目录环或多入口汇聚：节点只校验一次。
		}
		seen[node.ID] = true
		for _, ent := range node.Entries {
			child, ok := tree.Nodes[ent.ChildID]
			if !ok {
				return ErrUnknownNode
			}
			if err := validate(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validate(root); err != nil {
		return nil, err
	}
	for _, ref := range tree.OutsideRefs {
		if ref.Name == "" {
			return nil, errors.New("netsec: outside anchor name is empty")
		}
		if _, ok := tree.Nodes[ref.ChildID]; !ok {
			return nil, ErrUnknownNode
		}
	}

	c := &Checker{
		tree:    tree,
		allowed: make(map[ObjectID]struct{}, len(allowed)),
		states:  make(map[ObjectID]*objState),
	}
	for id := range allowed {
		c.allowed[id] = struct{}{}
	}
	c.build()
	return c, nil
}

// bfsSource 是一次最短路径搜索的入口。
type bfsSource struct {
	nodeID string
	path   []string
}

// shortestPathMap 从若干入口出发在节点图上做标准 BFS，
// 返回每个可达对象的最短可达路径。
//
// 第一步按节点求规范最短路径：节点首次被访问时的路径即最短路径
// （目录环由 visited 集合截断，保证终止）；每个节点的子项按名字排序，
// 等长路径取字典序最小者，结果确定可复现。
// 第二步再按对象聚合：同一对象的多个硬链接节点各自持有自己的最短
// 路径，取其中最小者作为该对象的最短路径。节点级剪枝与对象级取最小值
// 是两个独立步骤，互不干扰。
func (c *Checker) shortestPathMap(sources []bfsSource) map[ObjectID][]string {
	nodePath := make(map[string][]string)
	dist := make(map[ObjectID][]string)
	queue := make([]bfsSource, 0, len(sources))
	for _, src := range sources {
		if _, ok := nodePath[src.nodeID]; ok {
			continue
		}
		path := append([]string(nil), src.path...)
		nodePath[src.nodeID] = path
		queue = append(queue, bfsSource{nodeID: src.nodeID, path: path})
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		node := c.tree.Nodes[cur.nodeID]
		if !node.IsDir {
			continue
		}
		entries := append([]Entry(nil), node.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, ent := range entries {
			if _, ok := nodePath[ent.ChildID]; ok {
				continue
			}
			next := bfsSource{
				nodeID: ent.ChildID,
				path:   append(append([]string(nil), cur.path...), ent.Name),
			}
			nodePath[next.nodeID] = next.path
			queue = append(queue, next)
		}
	}
	for nodeID, path := range nodePath {
		obj := c.tree.Nodes[nodeID].Object
		dist[obj] = minPath(dist[obj], path)
	}
	return dist
}

// build 从隔离区根与宿主侧锚点各做一次 BFS，
// 汇总得到每个对象两侧的最短路径。
func (c *Checker) build() {
	inside := c.shortestPathMap([]bfsSource{{nodeID: c.tree.RootID, path: []string{}}})

	outsideSources := make([]bfsSource, 0, len(c.tree.OutsideRefs))
	for _, ref := range c.tree.OutsideRefs {
		outsideSources = append(outsideSources, bfsSource{
			nodeID: ref.ChildID,
			path:   []string{ref.Name},
		})
	}
	sort.Slice(outsideSources, func(i, j int) bool {
		return lessPath(outsideSources[i].path, outsideSources[j].path)
	})
	outside := c.shortestPathMap(outsideSources)

	for _, node := range c.tree.Nodes {
		st := c.states[node.Object]
		if st == nil {
			st = &objState{}
			c.states[node.Object] = st
		}
		if path, ok := inside[node.Object]; ok {
			st.inside = minPath(st.inside, path)
		}
		if path, ok := outside[node.Object]; ok {
			st.outside = minPath(st.outside, path)
		}
	}
}

// shortestToObject 在当前图上只针对一个对象做最短路径搜索，
// 供 Unlink 后增量重算受影响对象使用，不触碰其他对象的状态。
func (c *Checker) shortestToObject(sources []bfsSource, target ObjectID) []string {
	visited := make(map[string]bool)
	queue := make([]bfsSource, 0, len(sources))
	for _, src := range sources {
		if visited[src.nodeID] {
			continue
		}
		visited[src.nodeID] = true
		queue = append(queue, bfsSource{
			nodeID: src.nodeID,
			path:   append([]string(nil), src.path...),
		})
	}

	var best []string
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		node := c.tree.Nodes[cur.nodeID]
		if node.Object == target {
			// 同一对象可能由多个硬链接节点承载，需在全部最短命中
			// 候选中取字典序最小者，因此与 shortestPathMap 一样
			// 只做记录、不提前返回。
			best = minPath(best, cur.path)
		}
		if !node.IsDir {
			continue
		}
		entries := append([]Entry(nil), node.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, ent := range entries {
			if visited[ent.ChildID] {
				continue
			}
			visited[ent.ChildID] = true
			queue = append(queue, bfsSource{
				nodeID: ent.ChildID,
				path:   append(append([]string(nil), cur.path...), ent.Name),
			})
		}
	}
	return best
}

// Unlink 断开 parentID 目录下名为 name 的一条硬链接（目录项）。
// 目录项的断开只改变被断开节点所引用对象的可达性，因此该方法只重算
// 这一个对象的两侧最短路径与违规状态，其他对象的缓存状态保持不变。
// 不支持断开目录类型的目录项：目录项的移除会改变整棵子树的可达性，
// 不属于“断开一条文件硬链接”的增量场景，此时返回 ErrNotDirectory。
func (c *Checker) Unlink(parentID, name string) error {
	parent, ok := c.tree.Nodes[parentID]
	if !ok {
		return ErrUnknownNode
	}
	if !parent.IsDir {
		return ErrNotDirectory
	}

	idx := -1
	for i, ent := range parent.Entries {
		if ent.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrEntryNotFound
	}

	target := c.tree.Nodes[parent.Entries[idx].ChildID]
	if target.IsDir {
		return ErrNotDirectory
	}
	obj := target.Object

	parent.Entries = append(parent.Entries[:idx], parent.Entries[idx+1:]...)
	c.refresh(obj)
	return nil
}

// refresh 只重算单个对象的两侧最短路径，并在对象失去所有引用时清理其状态。
func (c *Checker) refresh(obj ObjectID) {
	insidePath := c.shortestToObject(
		[]bfsSource{{nodeID: c.tree.RootID, path: []string{}}},
		obj,
	)

	outsideSources := make([]bfsSource, 0, len(c.tree.OutsideRefs))
	for _, ref := range c.tree.OutsideRefs {
		outsideSources = append(outsideSources, bfsSource{
			nodeID: ref.ChildID,
			path:   []string{ref.Name},
		})
	}
	sort.Slice(outsideSources, func(i, j int) bool {
		return lessPath(outsideSources[i].path, outsideSources[j].path)
	})
	outsidePath := c.shortestToObject(outsideSources, obj)

	if insidePath == nil && outsidePath == nil {
		delete(c.states, obj)
		return
	}
	c.states[obj] = &objState{inside: insidePath, outside: outsidePath}
}

// Reports 返回当前所有未经允许的跨域共享对象报告，
// 按对象号、代次排序。返回的路径为副本，调用方可自由修改。
func (c *Checker) Reports() []Report {
	reports := make([]Report, 0)
	for obj, st := range c.states {
		if _, ok := c.allowed[obj]; ok {
			continue
		}
		if st.inside == nil || st.outside == nil {
			continue
		}
		reports = append(reports, Report{
			Object:      obj,
			InsidePath:  append([]string(nil), st.inside...),
			OutsidePath: append([]string(nil), st.outside...),
		})
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Object.Number != reports[j].Object.Number {
			return reports[i].Object.Number < reports[j].Object.Number
		}
		return reports[i].Object.Generation < reports[j].Object.Generation
	})
	return reports
}

// minPath 在两条候选路径中取更短者；等长时取字典序更小者。
// nil 表示当前尚无路径。
func minPath(current, candidate []string) []string {
	if current == nil || lessPath(candidate, current) {
		return append([]string(nil), candidate...)
	}
	return current
}

// lessPath 先按路径长度（深度）比较，长度相同时再按路径段字典序比较。
func lessPath(a, b []string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	n := len(a)
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
