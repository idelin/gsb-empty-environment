// Package crdt 实现树形层次文件系统的无环 CRDT 重命名与移动协调器。
//
// 核心思想:
//   - 每个移动/重命名操作携带 Lamport 逻辑时间戳与副本 ID,构成确定性全序
//     (Lamport 升序,平局时按 ReplicaID 字典序)。
//   - 所有副本收敛后,按全序重放操作日志,从基准树形状态重建物化视图。
//   - 重放时沿父链做因果追溯:若目标父节点是被移动节点自身或其后代,
//     应用该操作将产生拓扑环,则确定性撤销(拒绝)该操作——在全序中
//     “后发生”的成环操作必然被拒绝,先发生的操作保留,目录不丢失。
package crdt

import (
	"fmt"
	"sort"
	"strings"
)

// ReplicaID 唯一标识一个分布式节点(副本)。
type ReplicaID string

// NodeID 唯一标识树中的一个目录/文件节点。
type NodeID string

// RootID 是虚拟目录树的根,根不可被移动。
const RootID NodeID = "root"

// Timestamp 是带副本 ID 的 Lamport 时间戳,用于确定性全序仲裁。
type Timestamp struct {
	Lamport uint64
	Replica ReplicaID
}

// Less 定义确定性全序:先比 Lamport 时钟,平局比副本 ID。
func (t Timestamp) Less(o Timestamp) bool {
	if t.Lamport != o.Lamport {
		return t.Lamport < o.Lamport
	}
	return t.Replica < o.Replica
}

func (t Timestamp) String() string {
	return fmt.Sprintf("%d@%s", t.Lamport, t.Replica)
}

// MoveOp 表示一次移动/重命名操作。NewName 为空表示仅移动不改名。
type MoveOp struct {
	Node      NodeID
	NewParent NodeID
	NewName   string
	Time      Timestamp
}

// ID 返回操作的全局唯一标识(Lamport, Replica)。
func (op MoveOp) ID() Timestamp { return op.Time }

// node 是物化视图中的树节点。
type node struct {
	name     string
	parent   NodeID
	children map[NodeID]bool
}

// RejectedOp 记录一条在重放时被仲裁撤销的操作及原因。
type RejectedOp struct {
	Op     MoveOp
	Reason string
}

// Replica 是一个 CRDT 副本:持有操作日志,并可重放出无环物化视图。
type Replica struct {
	id    ReplicaID
	clock uint64

	// base 是基准树形状态(系统初始目录结构),所有副本一致。
	base map[NodeID]node

	// log 是已知移动操作日志(含本地与合并来的远程操作),按 ID 去重。
	log    map[Timestamp]MoveOp
	sorted []MoveOp // log 的确定性全序缓存
	dirty  bool

	// 物化视图缓存
	view     map[NodeID]node
	rejected []RejectedOp
}

// NewReplica 创建副本。base 为初始目录结构(父指针表),
// 所有副本必须以相同的 base 启动以保证收敛。
func NewReplica(id ReplicaID, base map[NodeID]node) *Replica {
	b := make(map[NodeID]node, len(base))
	for k, v := range base {
		b[k] = v
	}
	return &Replica{id: id, base: b, log: make(map[Timestamp]MoveOp), dirty: true}
}

// BaseNode 用于声明初始目录结构。
type BaseNode struct {
	Name   string
	Parent NodeID // 根的 Parent 为 ""
}

// NewBase 从声明式描述构建基准树。
func NewBase(entries map[NodeID]BaseNode) map[NodeID]node {
	base := make(map[NodeID]node, len(entries))
	for id, e := range entries {
		base[id] = node{name: e.Name, parent: e.Parent, children: map[NodeID]bool{}}
	}
	for id, e := range entries {
		if e.Parent != "" {
			p := base[e.Parent]
			if p.children == nil {
				p.children = map[NodeID]bool{}
			}
			p.children[id] = true
			base[e.Parent] = p
		}
	}
	return base
}

// Move 在本地产生一次移动/重命名操作:newParent 为目标父目录,
// newName 为空字符串表示保持原名。返回该操作的时间戳。
func (r *Replica) Move(node NodeID, newParent NodeID, newName string) Timestamp {
	r.clock++
	ts := Timestamp{Lamport: r.clock, Replica: r.id}
	r.log[ts] = MoveOp{Node: node, NewParent: newParent, NewName: newName, Time: ts}
	r.dirty = true
	return ts
}

// Merge 合并另一个副本的操作日志(可重复调用,幂等)。
// 合并后本地逻辑时钟推进到已知最大值,保证后续本地操作在全序中靠后。
func (r *Replica) Merge(other *Replica) {
	other.recompute()
	for _, op := range other.sorted {
		if _, ok := r.log[op.ID()]; !ok {
			r.log[op.ID()] = op
			r.dirty = true
		}
	}
	for _, op := range r.log {
		if op.Time.Lamport > r.clock {
			r.clock = op.Time.Lamport
		}
	}
}

// recompute 按确定性全序重放日志,重建无环物化视图。
func (r *Replica) recompute() {
	if !r.dirty {
		return
	}
	r.sorted = r.sorted[:0]
	for _, op := range r.log {
		r.sorted = append(r.sorted, op)
	}
	sort.Slice(r.sorted, func(i, j int) bool {
		return r.sorted[i].Time.Less(r.sorted[j].Time)
	})

	// 从基准状态重建
	view := make(map[NodeID]node, len(r.base))
	for id, n := range r.base {
		view[id] = node{name: n.name, parent: n.parent, children: map[NodeID]bool{}}
	}
	for id, n := range r.base {
		if n.parent != "" {
			p := view[n.parent]
			p.children[id] = true
			view[n.parent] = p
		}
	}

	r.rejected = r.rejected[:0]
	for _, op := range r.sorted {
		if reason := applyOp(view, op); reason != "" {
			r.rejected = append(r.rejected, RejectedOp{Op: op, Reason: reason})
		}
	}
	r.view = view
	r.dirty = false
}

// applyOp 在物化视图上应用单条操作;返回空串表示成功,否则为拒绝原因。
func applyOp(view map[NodeID]node, op MoveOp) string {
	n, ok := view[op.Node]
	if !ok {
		return "目标节点不存在"
	}
	if op.Node == RootID {
		return "根节点不可移动"
	}
	p, ok := view[op.NewParent]
	if !ok {
		return "目标父目录不存在"
	}
	if op.NewParent == op.Node {
		return "不能将目录移入自身"
	}
	// 父节点因果追溯:沿父链向上,若遇到被移动节点则成环。
	for cur := op.NewParent; cur != ""; {
		if cur == op.Node {
			return fmt.Sprintf("检测到拓扑依赖环: %s 是 %s 的后代", op.NewParent, op.Node)
		}
		cur = view[cur].parent
	}
	_ = p

	// 从旧父摘除
	if n.parent != "" {
		old := view[n.parent]
		delete(old.children, op.Node)
		view[n.parent] = old
	}
	// 同名冲突确定性消解:追加 @副本 后缀(全序平局键)。
	name := n.name
	if op.NewName != "" {
		name = op.NewName
	}
	if conflictName(view, op.NewParent, name) {
		name = name + "@" + string(op.Time.Replica)
	}
	// 挂到新父
	n.parent = op.NewParent
	n.name = name
	view[op.Node] = n
	p.children[op.Node] = true
	view[op.NewParent] = p
	return ""
}

func conflictName(view map[NodeID]node, parent NodeID, name string) bool {
	for child := range view[parent].children {
		if view[child].name == name {
			return true
		}
	}
	return false
}

// Rejected 返回重放中被撤销的操作及原因(用于可观测性/审计)。
func (r *Replica) Rejected() []RejectedOp {
	r.recompute()
	out := make([]RejectedOp, len(r.rejected))
	copy(out, r.rejected)
	return out
}

// Log 返回按确定性全序排序的操作日志副本。
func (r *Replica) Log() []MoveOp {
	r.recompute()
	out := make([]MoveOp, len(r.sorted))
	copy(out, r.sorted)
	return out
}

// Path 返回节点的绝对路径;节点不存在时返回空串与 false。
func (r *Replica) Path(id NodeID) (string, bool) {
	r.recompute()
	if _, ok := r.view[id]; !ok {
		return "", false
	}
	var parts []string
	for cur := id; cur != RootID; {
		n := r.view[cur]
		parts = append([]string{n.name}, parts...)
		cur = n.parent
	}
	return "/" + strings.Join(parts, "/"), true
}

// Parent 返回节点的父节点 ID。
func (r *Replica) Parent(id NodeID) (NodeID, bool) {
	r.recompute()
	n, ok := r.view[id]
	return n.parent, ok
}

// Snapshot 返回 id -> (名称, 父节点) 的物化视图快照,用于收敛一致性比较。
func (r *Replica) Snapshot() map[NodeID][2]string {
	r.recompute()
	out := make(map[NodeID][2]string, len(r.view))
	for id, n := range r.view {
		out[id] = [2]string{n.name, string(n.parent)}
	}
	return out
}

// HasCycle 校验物化视图无环(不变式自检)。
func (r *Replica) HasCycle() bool {
	r.recompute()
	for id := range r.view {
		seen := map[NodeID]bool{}
		for cur := id; cur != ""; {
			if seen[cur] {
				return true
			}
			seen[cur] = true
			cur = r.view[cur].parent
		}
	}
	return false
}
