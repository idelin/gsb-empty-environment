// Package crdt 实现树形层次文件系统的无环 CRDT 重命名与移动协调器。
//
// 核心思想：
//   - 每个 move/rename 操作携带 Lamport 时间戳 + 副本 ID，构成确定性全序 OpID。
//   - 所有副本收敛于同一份操作日志；合并时按 OpID 全序重放日志。
//   - 重放时对每个操作做父节点因果追溯（沿当前父链上溯），
//     若应用该操作会把目录移入自己的子孙（成环），则仲裁撤销该操作。
//   - 由于全序确定、日志相同，所有副本仲裁结果一致，后发生的成环操作被撤销，
//     目录不会丢失、不会形成孤立环。
package crdt

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RootID 是树根节点的固定 ID。
const RootID = "root"

// OpID 是操作的全局唯一标识，(Lamport, Replica) 构成确定性全序。
type OpID struct {
	Lamport uint64 // Lamport 逻辑时钟
	Replica string // 发起副本 ID，作为时钟相同时的决胜项
}

// Less 定义 OpID 的确定性全序。
func (a OpID) Less(b OpID) bool {
	if a.Lamport != b.Lamport {
		return a.Lamport < b.Lamport
	}
	return a.Replica < b.Replica
}

func (a OpID) String() string { return fmt.Sprintf("%d@%s", a.Lamport, a.Replica) }

// MoveOp 表示一次移动（可选同时重命名）操作。
type MoveOp struct {
	ID        OpID   // 操作标识（确定性全序）
	Child     string // 被移动的节点 ID
	NewParent string // 目标父节点 ID
	Name      string // 非空则同时重命名
}

func (op MoveOp) String() string {
	if op.Name != "" {
		return fmt.Sprintf("move(%s -> %s, rename %q, %s)", op.Child, op.NewParent, op.Name, op.ID)
	}
	return fmt.Sprintf("move(%s -> %s, %s)", op.Child, op.NewParent, op.ID)
}

type node struct {
	id       string
	name     string
	baseName string // 创建时的名字（重放日志的起点）
	basePar  string // 创建时的父节点（重放日志的起点）
	parent   string // 当前生效的父节点
	children map[string]bool
}

// Tree 是单个副本上的目录树 CRDT 协调器。
type Tree struct {
	replica string
	clock   uint64
	nodes   map[string]*node
	log     []MoveOp        // 已知操作日志（按 OpID 全序）
	seen    map[OpID]bool   // 去重，保证 Merge 幂等
	undone  map[OpID]MoveOp // 被仲裁撤销的操作
}

// NewTree 创建一个副本协调器，自动包含根节点 "/"。
func NewTree(replica string) *Tree {
	t := &Tree{
		replica: replica,
		nodes:   map[string]*node{},
		seen:    map[OpID]bool{},
		undone:  map[OpID]MoveOp{},
	}
	t.nodes[RootID] = &node{id: RootID, name: "/", baseName: "/", children: map[string]bool{}}
	return t
}

var (
	ErrNotFound  = errors.New("crdt: node not found")
	ErrNameClash = errors.New("crdt: name already exists under parent")
	ErrMoveRoot  = errors.New("crdt: cannot move the root")
	ErrInvalidOp = errors.New("crdt: invalid operation")
)

// Mkdir 在 parentID 下创建目录，id 由调用方指定以便各副本引导出相同初始树。
func (t *Tree) Mkdir(parentID, id, name string) error {
	p, ok := t.nodes[parentID]
	if !ok {
		return fmt.Errorf("%w: parent %q", ErrNotFound, parentID)
	}
	if _, dup := t.nodes[id]; dup {
		return fmt.Errorf("%w: id %q", ErrNameClash, id)
	}
	for c := range p.children {
		if t.nodes[c].name == name {
			return fmt.Errorf("%w: %q under %q", ErrNameClash, name, parentID)
		}
	}
	t.nodes[id] = &node{id: id, name: name, baseName: name, basePar: parentID, parent: parentID, children: map[string]bool{}}
	p.children[id] = true
	return nil
}

// Move 在本地发起移动操作：记录日志、推进 Lamport 时钟并重放。
func (t *Tree) Move(childID, newParentID string) (MoveOp, error) {
	return t.MoveRename(childID, newParentID, "")
}

// MoveRename 在本地发起移动+重命名操作。
func (t *Tree) MoveRename(childID, newParentID, name string) (MoveOp, error) {
	if childID == RootID {
		return MoveOp{}, ErrMoveRoot
	}
	if _, ok := t.nodes[childID]; !ok {
		return MoveOp{}, fmt.Errorf("%w: child %q", ErrNotFound, childID)
	}
	if _, ok := t.nodes[newParentID]; !ok {
		return MoveOp{}, fmt.Errorf("%w: parent %q", ErrNotFound, newParentID)
	}
	t.clock++
	op := MoveOp{ID: OpID{Lamport: t.clock, Replica: t.replica}, Child: childID, NewParent: newParentID, Name: name}
	t.append(op)
	t.replay()
	return op, nil
}

// Merge 合并来自其他副本的操作日志。幂等、可交换、可结合。
func (t *Tree) Merge(ops []MoveOp) {
	for _, op := range ops {
		if op.ID.Lamport > t.clock {
			t.clock = op.ID.Lamport // Lamport 时钟因果推进
		}
		if !t.seen[op.ID] {
			t.append(op)
		}
	}
	t.replay()
}

// Ops 返回本地已知日志的副本，用于向其他副本复制。
func (t *Tree) Ops() []MoveOp {
	out := make([]MoveOp, len(t.log))
	copy(out, t.log)
	return out
}

// Undone 返回被仲裁撤销（为断环而未生效）的操作。
func (t *Tree) Undone() []MoveOp {
	out := make([]MoveOp, 0, len(t.undone))
	for _, op := range t.undone {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Less(out[j].ID) })
	return out
}

func (t *Tree) append(op MoveOp) {
	t.seen[op.ID] = true
	i := sort.Search(len(t.log), func(i int) bool { return op.ID.Less(t.log[i].ID) })
	t.log = append(t.log, MoveOp{})
	copy(t.log[i+1:], t.log[i:])
	t.log[i] = op
}

// replay 按确定性全序重放整个日志，重建生效状态。
// 对每个操作做父节点因果追溯：若新父节点是被移动节点的子孙，则成环，
// 仲裁撤销该操作（全序中较晚者被撤销），保证树无环且无目录丢失。
func (t *Tree) replay() {
	for _, n := range t.nodes {
		n.name = n.baseName
		n.parent = n.basePar
		n.children = map[string]bool{}
	}
	for _, n := range t.nodes {
		if n.basePar != "" {
			t.nodes[n.basePar].children[n.id] = true
		}
	}
	t.undone = map[OpID]MoveOp{}
	for _, op := range t.log {
		if !t.applyValid(op) {
			t.undone[op.ID] = op
		}
	}
}

// applyValid 尝试应用单个操作，返回是否生效。
func (t *Tree) applyValid(op MoveOp) bool {
	child, ok := t.nodes[op.Child]
	if !ok || op.Child == RootID {
		return false
	}
	parent, ok := t.nodes[op.NewParent]
	if !ok {
		return false
	}
	// 父节点因果追溯：沿当前父链从新父节点上溯，遇到被移动节点即成环。
	for cur, guard := parent.id, map[string]bool{}; cur != ""; {
		if cur == child.id {
			return false // 会把目录移入自己的子孙 -> 撤销
		}
		if guard[cur] {
			break
		}
		guard[cur] = true
		cur = t.nodes[cur].parent
	}
	// 同目录下重名检测：保留先者，撤销后者。
	name := op.Name
	if name == "" {
		name = child.name
	}
	for sib := range parent.children {
		if sib != child.id && t.nodes[sib].name == name {
			return false
		}
	}
	delete(t.nodes[child.parent].children, child.id)
	child.parent = parent.id
	child.name = name
	parent.children[child.id] = true
	return true
}

// Lookup 按绝对路径（如 "/dirA/dirB"）解析节点 ID。
func (t *Tree) Lookup(path string) (string, error) {
	if path == "/" {
		return RootID, nil
	}
	cur := t.nodes[RootID]
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		next := ""
		for c := range cur.children {
			if t.nodes[c].name == seg {
				next = c
				break
			}
		}
		if next == "" {
			return "", fmt.Errorf("%w: path %q", ErrNotFound, path)
		}
		cur = t.nodes[next]
	}
	return cur.id, nil
}

// Path 返回节点的当前绝对路径。
func (t *Tree) Path(id string) (string, error) {
	n, ok := t.nodes[id]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	var segs []string
	for cur := n; cur.id != RootID; cur = t.nodes[cur.parent] {
		segs = append([]string{cur.name}, segs...)
	}
	return "/" + strings.Join(segs, "/"), nil
}

// Contains 报告 id 节点是否存在（用于验证无目录丢失）。
func (t *Tree) Contains(id string) bool {
	_, ok := t.nodes[id]
	return ok
}

// IsAcyclic 校验从每个节点沿父链都能到达根（无孤立环）。
func (t *Tree) IsAcyclic() bool {
	for id := range t.nodes {
		cur, guard := id, map[string]bool{}
		for cur != RootID {
			if guard[cur] {
				return false
			}
			guard[cur] = true
			n := t.nodes[cur]
			if n.parent == "" {
				return false
			}
			cur = n.parent
		}
	}
	return true
}

// String 以缩进树形打印当前目录结构。
func (t *Tree) String() string {
	var b strings.Builder
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		n := t.nodes[id]
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", depth), n.name)
		kids := make([]string, 0, len(n.children))
		for c := range n.children {
			kids = append(kids, c)
		}
		sort.Slice(kids, func(i, j int) bool { return t.nodes[kids[i]].name < t.nodes[kids[j]].name })
		for _, c := range kids {
			walk(c, depth+1)
		}
	}
	walk(RootID, 0)
	return b.String()
}
